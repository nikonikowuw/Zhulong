import { AlertCircle, Network, RefreshCw } from 'lucide-react'
import { Alert, AlertDescription, AlertTitle } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { type InterfaceInfo } from '../api/network-api'
import { InterfaceCard } from './interface-card'

interface InterfaceListProps {
  interfaces?: InterfaceInfo[]
  isLoading: boolean
  isError: boolean
  error: Error | null
  onRefetch: () => void
  onEdit: (iface: InterfaceInfo) => void
  onPing: (target?: string) => void
}

export function InterfaceList({
  interfaces,
  isLoading,
  isError,
  error,
  onRefetch,
  onEdit,
  onPing,
}: InterfaceListProps) {
  if (isLoading) {
    return (
      <div className='grid grid-cols-1 gap-4 lg:grid-cols-2'>
        {[1, 2].map((i) => (
          <Card key={i} className='p-6'>
            <div className='flex items-center justify-between pb-4'>
              <div className='flex items-center gap-3'>
                <Skeleton className='h-10 w-10 rounded-lg' />
                <div className='space-y-1.5'>
                  <Skeleton className='h-4 w-28' />
                  <Skeleton className='h-3 w-40' />
                </div>
              </div>
              <Skeleton className='h-6 w-16 rounded-md' />
            </div>
            <Skeleton className='h-24 w-full rounded-lg' />
            <div className='mt-4 flex justify-between pt-2'>
              <Skeleton className='h-8 w-24' />
              <Skeleton className='h-8 w-24' />
            </div>
          </Card>
        ))}
      </div>
    )
  }

  if (isError) {
    return (
      <Alert variant='destructive' className='my-4'>
        <AlertCircle className='h-4 w-4' />
        <AlertTitle>获取网络接口失败</AlertTitle>
        <AlertDescription className='flex items-center justify-between'>
          <span>{error?.message || '无法连接至宿主系统网络服务'}</span>
          <Button
            variant='outline'
            size='sm'
            onClick={onRefetch}
            className='ml-4 gap-1.5 bg-background text-foreground'
          >
            <RefreshCw className='h-3.5 w-3.5' />
            重试
          </Button>
        </AlertDescription>
      </Alert>
    )
  }

  if (!interfaces || interfaces.length === 0) {
    return (
      <Card className='border-dashed'>
        <CardContent className='flex flex-col items-center justify-center py-12 text-center'>
          <div className='rounded-full bg-muted p-4 text-muted-foreground'>
            <Network className='h-8 w-8' />
          </div>
          <h3 className='mt-4 text-lg font-semibold'>未探测到物理网络接口</h3>
          <p className='mt-1.5 max-w-sm text-sm text-muted-foreground'>
            系统未能读取到任何可配置的物理以太网卡，请检查宿主机硬件或驱动服务。
          </p>
          <Button
            variant='outline'
            size='sm'
            onClick={onRefetch}
            className='mt-4 gap-1.5'
          >
            <RefreshCw className='h-3.5 w-3.5' />
            重新探测
          </Button>
        </CardContent>
      </Card>
    )
  }

  return (
    <div className='grid grid-cols-1 gap-4 lg:grid-cols-2'>
      {interfaces.map((iface) => (
        <InterfaceCard
          key={iface.name}
          iface={iface}
          onEdit={onEdit}
          onPing={onPing}
        />
      ))}
    </div>
  )
}
