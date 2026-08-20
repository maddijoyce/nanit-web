import useSWR from 'swr'
import { api } from '@/lib/api'
import type { StreamingInfoResponse } from '@/types/api'

export function useStreamingInfo(enabled: boolean = true) {
  const { data, error, isLoading } = useSWR<StreamingInfoResponse>(
    enabled ? '/streaming/info' : null,
    () => api.getStreamingInfo(),
    {
      // Server configuration, it only changes on restart
      revalidateOnFocus: false,
      dedupingInterval: 60000,
    }
  )

  return {
    streamingInfo: data,
    isLoading,
    isError: !!error,
    error,
  }
}
