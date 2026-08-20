'use client'

import { useEffect, useState } from 'react'
import { mutate } from 'swr'
import { api } from '@/lib/api'
import type { Baby } from '@/types/api'
import LoadingSpinner from '@/components/ui/LoadingSpinner'

interface ControlPanelProps {
  baby: Baby
}

interface ControlButtonProps {
  onClick: () => Promise<void>
  disabled: boolean
  loading: boolean
  children: React.ReactNode
  variant?: 'primary' | 'secondary' | 'danger'
}

function ControlButton({ onClick, disabled, loading, children, variant = 'primary' }: ControlButtonProps) {
  const [feedback, setFeedback] = useState<'success' | 'error' | null>(null)

  const handleClick = async () => {
    try {
      await onClick()
      setFeedback('success')
      setTimeout(() => setFeedback(null), 2000)
    } catch (error) {
      console.error('Control action failed:', error)
      setFeedback('error')
      setTimeout(() => setFeedback(null), 3000)
    }
  }

  const getButtonClass = () => {
    if (feedback === 'success') return 'btn btn-success'
    if (feedback === 'error') return 'btn btn-danger'
    
    switch (variant) {
      case 'secondary':
        return 'btn btn-secondary'
      case 'danger':
        return 'btn btn-danger'
      default:
        return 'btn btn-primary'
    }
  }

  const getButtonContent = () => {
    if (loading) {
      return (
        <div className="flex items-center gap-2">
          <LoadingSpinner size="sm" />
          <span>Sending...</span>
        </div>
      )
    }
    
    if (feedback === 'success') return 'Sent!'
    if (feedback === 'error') return 'Failed'
    
    return children
  }

  return (
    <button
      onClick={handleClick}
      disabled={disabled || loading || !!feedback}
      className={`${getButtonClass()} disabled:opacity-50 disabled:cursor-not-allowed`}
    >
      {getButtonContent()}
    </button>
  )
}

export default function ControlPanel({ baby }: ControlPanelProps) {
  const [loadingStates, setLoadingStates] = useState<Record<string, boolean>>({})

  const setLoading = (action: string, loading: boolean) => {
    setLoadingStates(prev => ({ ...prev, [action]: loading }))
  }

  const handleNightLightToggle = async () => {
    setLoading('nightlight', true)
    try {
      await api.toggleNightLight(baby.uid)
      refreshStatus()
    } finally {
      setLoading('nightlight', false)
    }
  }

  const handleStandbyToggle = async () => {
    setLoading('standby', true)
    try {
      await api.toggleStandby(baby.uid)
      refreshStatus()
    } finally {
      setLoading('standby', false)
    }
  }

  // Track the slider locally so dragging stays responsive, but follow the
  // device whenever it reports a value we did not just set ourselves.
  const [volume, setVolume] = useState<number>(baby.volume ?? 0)

  useEffect(() => {
    if (typeof baby.volume === 'number') {
      setVolume(baby.volume)
    }
  }, [baby.volume])

  const handleVolumeCommit = async (level: number) => {
    setLoading('volume', true)
    try {
      await api.setVolume(baby.uid, level)
      refreshStatus()
    } catch (error) {
      console.error('Volume command failed:', error)
      // Snap back to the last value the device reported
      setVolume(baby.volume ?? 0)
    } finally {
      setLoading('volume', false)
    }
  }

  // The status endpoint is polled on an interval, but a command should show up
  // straight away. The second refresh catches the reconciled value: the backend
  // re-reads playback state from the camera about 1.5s after each command, so
  // an immediate refresh alone would show what was asked for rather than what
  // the camera did.
  const refreshStatus = () => {
    mutate('/status')
    setTimeout(() => mutate('/status'), 2000)
  }

  const soundtracks = baby.available_soundtracks ?? []
  const isSoundtrackPlaying = baby.soundtrack_playing ?? false

  const [soundtrack, setSoundtrack] = useState<string>(baby.soundtrack ?? 'Off')

  useEffect(() => {
    if (baby.soundtrack) {
      setSoundtrack(baby.soundtrack)
    }
  }, [baby.soundtrack])

  const handleSoundtrackSelect = async (name: string) => {
    setSoundtrack(name)
    setLoading('soundtrack', true)
    try {
      await api.setSoundtrack(baby.uid, name)
      refreshStatus()
    } catch (error) {
      console.error('Soundtrack command failed:', error)
      // Snap back to what the camera last reported
      setSoundtrack(baby.soundtrack ?? 'Off')
    } finally {
      setLoading('soundtrack', false)
    }
  }

  const handleSoundtrackToggle = async () => {
    setLoading('soundtrackToggle', true)
    try {
      await api.toggleSoundtrack(baby.uid)
      refreshStatus()
    } finally {
      setLoading('soundtrackToggle', false)
    }
  }

  const isDisabled = !baby.websocket_alive

  return (
    <div className="space-y-4">
      <h3 className="text-lg font-semibold text-nanit-gray-800">Controls</h3>
      
      {!baby.websocket_alive && (
        <div className="bg-yellow-50 border-l-4 border-yellow-400 p-3 rounded">
          <div className="text-sm text-yellow-800">
            ⚠️ Device is offline. Controls are disabled until connection is restored.
          </div>
        </div>
      )}
      
      <div className="flex flex-wrap gap-3">
        <ControlButton
          onClick={handleNightLightToggle}
          disabled={isDisabled}
          loading={loadingStates.nightlight || false}
        >
          {baby.night_light ? 'Turn Off Night Light' : 'Turn On Night Light'}
        </ControlButton>
        
        <ControlButton
          onClick={handleStandbyToggle}
          disabled={isDisabled}
          loading={loadingStates.standby || false}
          variant="secondary"
        >
          {baby.standby ? 'Exit Standby' : 'Enter Standby'}
        </ControlButton>

        <ControlButton
          onClick={handleSoundtrackToggle}
          disabled={isDisabled || (!isSoundtrackPlaying && soundtracks.length === 0)}
          loading={loadingStates.soundtrackToggle || false}
          variant="secondary"
        >
          {isSoundtrackPlaying ? '⏹ Stop Sound' : '▶ Play Sound'}
        </ControlButton>
      </div>
      
      <div className="space-y-1">
        <div className="flex items-center justify-between">
          <label htmlFor={`soundtrack-${baby.uid}`} className="text-sm font-medium text-nanit-gray-700">
            Sound
          </label>
          <span className="text-sm text-nanit-gray-600">
            {isSoundtrackPlaying ? 'Playing' : 'Stopped'}{loadingStates.soundtrack ? ' …' : ''}
          </span>
        </div>
        <select
          id={`soundtrack-${baby.uid}`}
          value={soundtrack}
          disabled={isDisabled || loadingStates.soundtrack || soundtracks.length === 0}
          onChange={(e) => handleSoundtrackSelect(e.target.value)}
          className="w-full rounded border border-nanit-gray-300 bg-white px-2 py-1 text-sm disabled:opacity-50 disabled:cursor-not-allowed"
        >
          <option value="Off">Off</option>
          {soundtracks.map((entry) => (
            <option key={entry.name} value={entry.name}>
              {entry.display_name}
            </option>
          ))}
        </select>
        {soundtracks.length === 0 && (
          <div className="text-xs text-nanit-gray-500">
            Waiting for the camera to report its available sounds.
          </div>
        )}
      </div>

      <div className="space-y-1">
        <div className="flex items-center justify-between">
          <label htmlFor={`volume-${baby.uid}`} className="text-sm font-medium text-nanit-gray-700">
            Volume
          </label>
          <span className="text-sm tabular-nums text-nanit-gray-600">
            {volume}%{loadingStates.volume ? ' …' : ''}
          </span>
        </div>
        <input
          id={`volume-${baby.uid}`}
          type="range"
          min={0}
          max={100}
          step={1}
          value={volume}
          disabled={isDisabled || loadingStates.volume}
          onChange={(e) => setVolume(Number(e.target.value))}
          onMouseUp={(e) => handleVolumeCommit(Number((e.target as HTMLInputElement).value))}
          onTouchEnd={(e) => handleVolumeCommit(Number((e.target as HTMLInputElement).value))}
          onKeyUp={(e) => handleVolumeCommit(Number((e.target as HTMLInputElement).value))}
          className="w-full disabled:opacity-50 disabled:cursor-not-allowed"
        />
      </div>

      <div className="text-xs text-nanit-gray-500">
        Control commands are sent to the device and may take a few seconds to take effect.
      </div>
    </div>
  )
}