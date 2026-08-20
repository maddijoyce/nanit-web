package client

import (
	"fmt"
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// UnknownField - a single field which was present on the wire but is absent
// from the generated schema.
//
// The Nanit protobuf definition in this repository is reverse-engineered and
// incomplete: several messages (notably Control and Response) carry field tags
// we have no mapping for. protobuf keeps those bytes rather than discarding
// them, which means an unmapped field can be read straight off a live camera
// instead of being guessed at.
type UnknownField struct {
	// Path - dotted location of the containing message, e.g. "response.control"
	Path string `json:"path"`
	// Tag - the protobuf field number
	Tag int32 `json:"tag"`
	// WireType - varint, fixed32, fixed64, bytes or group
	WireType string `json:"wire_type"`
	// Value - decoded scalar, or a printable rendering of a length-delimited field
	Value interface{} `json:"value"`
	// Nested - set when a length-delimited field itself parses as a message
	Nested []UnknownField `json:"nested,omitempty"`
}

// String - renders the field as "path.tag=value (wiretype)"
func (f UnknownField) String() string {
	return fmt.Sprintf("%v.%v=%v (%v)", f.Path, f.Tag, f.Value, f.WireType)
}

// DescribeUnknownFields - walks a message and every nested message reachable
// from it, decoding any field the schema does not map.
//
// This is the primary discovery tool for extending websocket.proto: run a
// request against a live camera, log the result, and the unmapped tags show up
// with their wire types and values.
func DescribeUnknownFields(m proto.Message) []UnknownField {
	if m == nil {
		return nil
	}

	out := make([]UnknownField, 0)
	collectUnknownFields(m.ProtoReflect(), string(m.ProtoReflect().Descriptor().Name()), &out)
	return out
}

func collectUnknownFields(m protoreflect.Message, path string, out *[]UnknownField) {
	if !m.IsValid() {
		return
	}

	if raw := m.GetUnknown(); len(raw) > 0 {
		*out = append(*out, parseRawFields(raw, path)...)
	}

	// Recurse into the fields we do understand: an unmapped tag may live on a
	// nested message (e.g. response.control) rather than at the top level.
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		switch {
		case fd.IsMap():
			// The Nanit schema uses no maps; nothing to walk.
		case fd.IsList() && isMessageKind(fd):
			list := v.List()
			for i := 0; i < list.Len(); i++ {
				collectUnknownFields(list.Get(i).Message(), fmt.Sprintf("%v.%v[%d]", path, fd.Name(), i), out)
			}
		case isMessageKind(fd):
			collectUnknownFields(v.Message(), fmt.Sprintf("%v.%v", path, fd.Name()), out)
		}

		return true
	})
}

func isMessageKind(fd protoreflect.FieldDescriptor) bool {
	return fd.Kind() == protoreflect.MessageKind || fd.Kind() == protoreflect.GroupKind
}

// parseRawFields - decodes a run of wire-format fields without a schema
func parseRawFields(raw []byte, path string) []UnknownField {
	fields := make([]UnknownField, 0)

	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			fields = append(fields, UnknownField{Path: path, WireType: "malformed", Value: "unable to consume tag"})
			return fields
		}
		raw = raw[n:]

		field := UnknownField{Path: path, Tag: int32(num)}

		switch typ {
		case protowire.VarintType:
			v, n := protowire.ConsumeVarint(raw)
			if n < 0 {
				field.WireType, field.Value = "malformed", "unable to consume varint"
				fields = append(fields, field)
				return fields
			}
			raw = raw[n:]
			field.WireType, field.Value = "varint", v

		case protowire.Fixed32Type:
			v, n := protowire.ConsumeFixed32(raw)
			if n < 0 {
				field.WireType, field.Value = "malformed", "unable to consume fixed32"
				fields = append(fields, field)
				return fields
			}
			raw = raw[n:]
			field.WireType, field.Value = "fixed32", v

		case protowire.Fixed64Type:
			v, n := protowire.ConsumeFixed64(raw)
			if n < 0 {
				field.WireType, field.Value = "malformed", "unable to consume fixed64"
				fields = append(fields, field)
				return fields
			}
			raw = raw[n:]
			field.WireType, field.Value = "fixed64", v

		case protowire.BytesType:
			v, n := protowire.ConsumeBytes(raw)
			if n < 0 {
				field.WireType, field.Value = "malformed", "unable to consume bytes"
				fields = append(fields, field)
				return fields
			}
			raw = raw[n:]
			field.WireType = "bytes"
			field.Value = describeBytes(v)

			// A length-delimited field is either a string, raw bytes or an
			// embedded message. Soundtrack names would arrive as strings;
			// a soundtrack list would arrive as embedded messages.
			if nested := parseNestedMessage(v, fmt.Sprintf("%v.%v", path, num)); nested != nil {
				field.Nested = nested
			}

		case protowire.StartGroupType:
			v, n := protowire.ConsumeGroup(num, raw)
			if n < 0 {
				field.WireType, field.Value = "malformed", "unable to consume group"
				fields = append(fields, field)
				return fields
			}
			raw = raw[n:]
			field.WireType = "group"
			field.Value = fmt.Sprintf("%d bytes", len(v))
			field.Nested = parseRawFields(v, fmt.Sprintf("%v.%v", path, num))

		default:
			field.WireType = "unsupported"
			field.Value = fmt.Sprintf("wire type %d", typ)
			fields = append(fields, field)
			return fields
		}

		fields = append(fields, field)
	}

	return fields
}

// describeBytes - renders a length-delimited payload as a quoted string when it
// is printable text, and as a byte count otherwise
func describeBytes(b []byte) string {
	if utf8.Valid(b) && isPrintable(b) {
		return strconv.Quote(string(b))
	}

	return fmt.Sprintf("<%d bytes>", len(b))
}

func isPrintable(b []byte) bool {
	if len(b) == 0 {
		return false
	}

	for _, r := range string(b) {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}

	return true
}

// parseNestedMessage - best-effort decode of a length-delimited payload as an
// embedded message. Returns nil when the bytes do not cleanly parse, which is
// the common case for plain strings.
func parseNestedMessage(b []byte, path string) []UnknownField {
	if len(b) == 0 {
		return nil
	}

	// Validate before reporting: a random string will often consume a tag or
	// two before falling apart, and reporting that as structure is misleading.
	rest := b
	for len(rest) > 0 {
		num, typ, n := protowire.ConsumeTag(rest)
		if n < 0 || num < 1 {
			return nil
		}
		rest = rest[n:]

		var size int
		switch typ {
		case protowire.VarintType:
			_, size = protowire.ConsumeVarint(rest)
		case protowire.Fixed32Type:
			_, size = protowire.ConsumeFixed32(rest)
		case protowire.Fixed64Type:
			_, size = protowire.ConsumeFixed64(rest)
		case protowire.BytesType:
			_, size = protowire.ConsumeBytes(rest)
		case protowire.StartGroupType:
			_, size = protowire.ConsumeGroup(num, rest)
		default:
			return nil
		}

		if size < 0 {
			return nil
		}
		rest = rest[size:]
	}

	return parseRawFields(b, path)
}

// SetUnknownVarintField - appends a varint field which the schema does not map.
//
// This is how a reverse-engineered control can be exercised before it has been
// added to websocket.proto: the bytes go on the wire exactly as a generated
// setter would place them, so a discovered tag can be tested without a
// regeneration cycle.
func SetUnknownVarintField(m proto.Message, tag int32, value uint64) {
	raw := m.ProtoReflect().GetUnknown()
	raw = protowire.AppendTag(raw, protowire.Number(tag), protowire.VarintType)
	raw = protowire.AppendVarint(raw, value)
	m.ProtoReflect().SetUnknown(raw)
}

// SetUnknownBytesField - appends a length-delimited field which the schema does
// not map. Used by the debug harness for candidate tags whose wire type is not
// a varint.
func SetUnknownBytesField(m proto.Message, tag int32, value []byte) {
	raw := m.ProtoReflect().GetUnknown()
	raw = protowire.AppendTag(raw, protowire.Number(tag), protowire.BytesType)
	raw = protowire.AppendBytes(raw, value)
	m.ProtoReflect().SetUnknown(raw)
}

// GetUnknownVarintField - reads the first varint carried under the given tag.
// Returns false when the message does not carry that tag.
func GetUnknownVarintField(m proto.Message, tag int32) (uint64, bool) {
	if m == nil {
		return 0, false
	}

	raw := m.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return 0, false
		}
		raw = raw[n:]

		var size int
		switch typ {
		case protowire.VarintType:
			v, vn := protowire.ConsumeVarint(raw)
			if vn < 0 {
				return 0, false
			}
			if int32(num) == tag {
				return v, true
			}
			size = vn
		case protowire.Fixed32Type:
			_, size = protowire.ConsumeFixed32(raw)
		case protowire.Fixed64Type:
			_, size = protowire.ConsumeFixed64(raw)
		case protowire.BytesType:
			_, size = protowire.ConsumeBytes(raw)
		case protowire.StartGroupType:
			_, size = protowire.ConsumeGroup(num, raw)
		default:
			return 0, false
		}

		if size < 0 {
			return 0, false
		}
		raw = raw[size:]
	}

	return 0, false
}

// GetUnknownBytesField - reads the first length-delimited payload carried under
// the given tag. Returns false when the message does not carry that tag.
func GetUnknownBytesField(m proto.Message, tag int32) ([]byte, bool) {
	if m == nil {
		return nil, false
	}

	raw := m.ProtoReflect().GetUnknown()
	for len(raw) > 0 {
		num, typ, n := protowire.ConsumeTag(raw)
		if n < 0 {
			return nil, false
		}
		raw = raw[n:]

		var size int
		switch typ {
		case protowire.VarintType:
			_, size = protowire.ConsumeVarint(raw)
		case protowire.Fixed32Type:
			_, size = protowire.ConsumeFixed32(raw)
		case protowire.Fixed64Type:
			_, size = protowire.ConsumeFixed64(raw)
		case protowire.BytesType:
			v, vn := protowire.ConsumeBytes(raw)
			if vn < 0 {
				return nil, false
			}
			if int32(num) == tag {
				return v, true
			}
			size = vn
		case protowire.StartGroupType:
			_, size = protowire.ConsumeGroup(num, raw)
		default:
			return nil, false
		}

		if size < 0 {
			return nil, false
		}
		raw = raw[size:]
	}

	return nil, false
}

// EncodeSelectorMessage - builds the body of an embedded message with the given
// field numbers set to boolean true.
//
// Several GET requests carry a selector sub-message of booleans (GetStatus.all,
// GetControl.nightLight and so on). When the selector itself is missing from the
// schema, this produces one that can be attached as an unknown field — which is
// how a request the camera rejects with "missed 'getX' field" can be satisfied
// without knowing the generated type.
func EncodeSelectorMessage(flags []int32) []byte {
	body := make([]byte, 0, len(flags)*2)
	for _, tag := range flags {
		if tag < 1 {
			continue
		}

		body = protowire.AppendTag(body, protowire.Number(tag), protowire.VarintType)
		body = protowire.AppendVarint(body, 1)
	}

	return body
}
