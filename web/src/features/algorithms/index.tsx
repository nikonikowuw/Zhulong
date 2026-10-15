import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { UploadCloud } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { AlgorithmUploadDialog } from './components/algorithm-upload-dialog'
import { AlgorithmsTable } from './components/algorithms-table'

export function Algorithms(): React.JSX.Element {
  const { t } = useTranslation('algorithms')
  const [uploadDialogOpen, setUploadDialogOpen] = useState(false)

  return (
    <>
      <Header fixed>
        <Search className='me-auto' />
        <LanguageSwitch />
        <ThemeSwitch />
        <ConfigDrawer />
        <ProfileDropdown />
      </Header>

      <Main className='flex flex-1 flex-col gap-4 sm:gap-6'>
        {/* 页面标题与主操作区 */}
        <div className='flex flex-wrap items-end justify-between gap-3'>
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>
              {t('pageTitle', '算法包管理')}
            </h2>
            <p className='text-sm text-muted-foreground'>
              {t(
                'pageDesc',
                '管理系统视觉计算与边缘推理算法包，支持安装、热启停、资源监控与安全卸载。'
              )}
            </p>
          </div>
          <Button
            className='gap-2 shadow-xs'
            onClick={() => setUploadDialogOpen(true)}
          >
            <UploadCloud className='h-4 w-4' />
            {t('uploadButton', '安装算法包')}
          </Button>
        </div>

        {/* 核心算法包列表 */}
        <AlgorithmsTable onOpenUpload={() => setUploadDialogOpen(true)} />
      </Main>

      {/* 上传安装弹窗 */}
      <AlgorithmUploadDialog
        open={uploadDialogOpen}
        onOpenChange={setUploadDialogOpen}
      />
    </>
  )
}
