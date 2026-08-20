'use client'

import { useState } from 'react'
import { api } from '@/lib/api'
import { useStreamingInfo } from '@/hooks/useStreamingInfo'
import { copyToClipboard } from '@/lib/utils'
import type { Baby } from '@/types/api'

interface StreamingLinksProps {
  baby: Baby
}

interface CopyButtonProps {
  text: string
  label: string
}

function CopyButton({ text, label }: CopyButtonProps) {
  const [copied, setCopied] = useState(false)

  const handleCopy = async () => {
    const success = await copyToClipboard(text)
    if (success) {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    }
  }

  return (
    <button
      onClick={handleCopy}
      className={`btn text-sm w-full ${copied ? 'btn-success' : 'btn-primary'}`}
    >
      {copied ? '✅ Copied!' : `📋 ${label}`}
    </button>
  )
}

export default function StreamingLinks({ baby }: StreamingLinksProps) {
  const [isExpanded, setIsExpanded] = useState(false)
  const { streamingInfo } = useStreamingInfo(isExpanded)

  const rtmpUrl = api.getRTMPUrl(baby.uid, streamingInfo)
  const hlsUrl = api.getHLSUrl(baby.uid)
  const rtmpDisabled = streamingInfo?.rtmp?.enabled === false

  return (
    <div className="border border-nanit-gray-200 rounded-lg overflow-hidden">
      <button
        onClick={() => setIsExpanded(!isExpanded)}
        className="w-full px-4 py-3 bg-nanit-gray-50 hover:bg-nanit-gray-100 transition-colors duration-200 flex items-center justify-between text-left"
      >
        <h3 className="font-semibold text-nanit-gray-800 flex items-center gap-2">
          🔗 Streaming Links
        </h3>
        <span className={`transform transition-transform duration-200 ${isExpanded ? 'rotate-90' : ''}`}>
          ▶
        </span>
      </button>
      
      {isExpanded && (
        <div className="p-4 space-y-4">
          <div className="grid md:grid-cols-2 gap-4">
            {/* RTMP Link */}
            <div className="bg-white border border-nanit-gray-200 rounded-lg p-4 border-l-4 border-l-red-500">
              <h4 className="font-semibold text-nanit-gray-800 mb-2 flex items-center gap-2">
                📡 RTMP Stream
              </h4>
              <p className="text-sm text-nanit-gray-600 mb-3">
                For Home Assistant, OBS, VLC, etc.
              </p>
              {rtmpDisabled ? (
                <p className="text-sm text-nanit-gray-700 bg-nanit-gray-50 p-3 rounded border">
                  The built-in RTMP server is disabled. Set{' '}
                  <span className="font-mono">NANIT_RTMP_ENABLED=true</span> and{' '}
                  <span className="font-mono">NANIT_RTMP_ADDR</span> to use it.
                </p>
              ) : (
                <>
                  <div className="bg-nanit-gray-50 p-3 rounded border text-sm font-mono text-nanit-gray-700 mb-3 overflow-x-auto whitespace-nowrap">
                    {rtmpUrl}
                  </div>
                  <CopyButton text={rtmpUrl} label="Copy RTMP URL" />
                </>
              )}
            </div>

            {/* HLS Link */}
            <div className="bg-white border border-nanit-gray-200 rounded-lg p-4 border-l-4 border-l-blue-500">
              <h4 className="font-semibold text-nanit-gray-800 mb-2 flex items-center gap-2">
                🌐 HLS Stream
              </h4>
              <p className="text-sm text-nanit-gray-600 mb-3">
                For web browsers and modern apps
              </p>
              <div className="bg-nanit-gray-50 p-3 rounded border text-sm font-mono text-nanit-gray-700 mb-3 overflow-x-auto whitespace-nowrap">
                {hlsUrl}
              </div>
              <CopyButton text={hlsUrl} label="Copy HLS URL" />
            </div>
          </div>

          <div className="bg-blue-50 border-l-4 border-blue-500 p-3 rounded">
            <div className="text-sm text-nanit-gray-700 space-y-1">
              <p><strong>Usage Notes:</strong></p>
              <p>• RTMP streams work with most video software and Home Assistant</p>
              <p>• HLS streams work in web browsers and mobile apps</p>
              <p>• Start the video stream above before using these URLs — the RTMP server rejects clients until the cam is publishing</p>
              <p>• The RTMP address comes from <span className="font-mono">NANIT_RTMP_ADDR</span> and must be reachable from the machine you play it on</p>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}