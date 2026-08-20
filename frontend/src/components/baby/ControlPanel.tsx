'use client'

import { useEffect, useState } from 'react'
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
      // Optionally refresh status here
    } finally {
      setLoading('nightlight', false)
    }
  }

  const handleStandbyToggle = async () => {
    setLoading('standby', true)
    try {
      await api.toggleStandby(baby.uid)
      // Optionally refresh status here
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
    } catch (error) {
      console.error('Volume command failed:', error)
      // Snap back to the last value the device reported
      setVolume(baby.volume ?? 0)
    } finally {
      setLoading('volume', false)
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