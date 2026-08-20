# Nanit Dashboard Frontend

Modern React + Next.js frontend for the Nanit Home Assistant Bridge.

## Features

- 🎯 **Real-time Dashboard** - Live sensor data with 5-second updates
- 📹 **Video Streaming** - HLS video player with live controls
- 📊 **Historical Data** - Charts and analytics (Chart.js integration ready)
- 🎛️ **Device Controls** - Night light, standby mode controls
- 🔧 **Device Information** - Detailed device status and diagnostics
- 🔗 **Streaming Links** - RTMP and HLS URLs for external use
- 📱 **Responsive Design** - Works on desktop, tablet, and mobile
- 🔐 **Authentication** - 2FA setup flow

## Technology Stack

- **Framework**: Next.js 14 with App Router
- **Language**: TypeScript
- **Styling**: Tailwind CSS
- **Data Fetching**: SWR for real-time updates
- **Video**: HLS.js for browser video streaming
- **Charts**: Ready for Chart.js integration
- **Build**: Static export for Go server integration

## Development Setup

### Prerequisites

- Node.js 18+
- npm or yarn

### Local Development

```bash
# Install dependencies
npm install

# Start development server
npm run dev

# Build for production
npm run build

# Export static files
npm run export
```

The development server will start on http://localhost:3000 and proxy API requests to the Go backend on port 8080.

## Docker Setup

### Build Frontend Container

```bash
# Build the frontend container
docker build -t nanit-frontend ./frontend

# Or use docker-compose (recommended)
docker-compose up --build frontend
```

### Integration with Go Backend

The frontend builds to static files that can be served by the Go backend:

1. **Development**: Next.js dev server proxies API calls to Go backend
2. **Production**: Static files are served by Go server from `frontend/dist`

### Environment Variables

- `NODE_ENV` - Set to 'production' for production builds
- API calls are proxied to the Go backend automatically

## Project Structure

```
frontend/
├── src/
│   ├── app/                 # Next.js App Router pages
│   ├── components/          # React components
│   │   ├── layout/         # Layout components
│   │   ├── baby/           # Baby-specific components
│   │   └── ui/             # Reusable UI components
│   ├── hooks/              # Custom React hooks
│   ├── lib/                # Utilities and API client
│   └── types/              # TypeScript type definitions
├── public/                 # Static assets
└── package.json           # Dependencies and scripts
```

## Key Components

### Baby Card (`components/baby/BabyCard.tsx`)
Main component displaying baby information including:
- Live video stream
- Sensor data (temperature, humidity, motion, sound)
- Device controls
- Historical data charts
- Streaming URLs

### API Client (`lib/api.ts`)
TypeScript client for all backend API calls with proper error handling and type safety.

### Hooks (`hooks/`)
- `useStatus.ts` - Real-time status updates with SWR
- `useTemperatureUnit.ts` - Temperature unit conversion

### Real-time Updates
Uses SWR with 5-second polling for live data updates. Automatically handles connection states and error recovery.

## API Integration

The frontend communicates with the Go backend through REST APIs:

- **Status**: `/api/status` - Real-time baby data
- **Controls**: `/api/control/*` - Device commands
- **History**: `/api/history/*` - Historical data
- **Streaming**: `/api/stream/*` - Video streaming
- **Auth**: `/api/auth/*` - Authentication

## Deployment

### Production Build

```bash
# Build and export static files
npm run build
npm run export
```

Static files are generated in `dist/` directory.

### Go Server Integration

Update your Go server to serve the frontend:

```go
// Serve frontend static files
http.Handle("/", http.FileServer(http.Dir("frontend/dist")))
```

### Docker Production

The repository root `Dockerfile` builds the frontend in its `frontend-build`
stage and copies the static export into the Go backend image, which serves it.

## Next Steps

1. **Chart Integration** - Implement Chart.js for historical data visualization
2. **Real-time WebSocket** - Add WebSocket for instant updates
3. **PWA Support** - Add service worker for offline functionality
4. **Mobile App** - Consider React Native for native mobile app

## Contributing

1. Follow TypeScript best practices
2. Use Tailwind CSS for styling
3. Ensure responsive design
4. Add proper error handling
5. Update types when API changes