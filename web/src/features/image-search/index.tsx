import { AlertTriangle } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { ImageSearchControls } from './components/image-search-controls'
import { ImageSearchDropzone } from './components/image-search-dropzone'
import { ImageSearchResults } from './components/image-search-results'
import { useImageSearch } from './hooks/use-image-search'

export function ImageSearch() {
  const { t } = useTranslation('imageSearch')
  const {
    selectedFile,
    previewUrl,
    threshold,
    setThreshold,
    topK,
    setTopK,
    uploadProgress,
    setFile,
    reset,
    search,
    isSearching,
    isError,
    error,
    results,
  } = useImageSearch()

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
        {/* 页面标题 */}
        <div className='flex flex-wrap items-end justify-between gap-2'>
          <div>
            <h2 className='text-2xl font-bold tracking-tight'>{t('title')}</h2>
            <p className='text-sm text-muted-foreground'>{t('subtitle')}</p>
          </div>
        </div>

        {/* 人工核验声明提示栏 */}
        <div className='flex items-center gap-2.5 rounded-lg border border-amber-500/30 bg-amber-500/10 px-3.5 py-2 text-xs text-amber-700 dark:text-amber-400'>
          <AlertTriangle className='h-4 w-4 shrink-0' />
          <span>{t('disclaimer')}</span>
        </div>

        {/* 主工作区分割：左侧/上侧为上传与配置，右侧/下侧为结果展示 */}
        <div className='grid grid-cols-1 gap-6 lg:grid-cols-12'>
          {/* 控制面板 */}
          <div className='flex flex-col gap-4 lg:col-span-4 xl:col-span-3'>
            <ImageSearchDropzone
              selectedFile={selectedFile}
              previewUrl={previewUrl}
              onFileSelect={setFile}
              disabled={isSearching}
            />

            <ImageSearchControls
              threshold={threshold}
              onThresholdChange={setThreshold}
              topK={topK}
              onTopKChange={setTopK}
              onSearch={search}
              onReset={reset}
              canSearch={!!selectedFile}
              isSearching={isSearching}
              uploadProgress={uploadProgress}
            />
          </div>

          {/* 检索结果展示面板 */}
          <div className='flex flex-col gap-4 lg:col-span-8 xl:col-span-9'>
            <ImageSearchResults
              results={results}
              isSearching={isSearching}
              isError={isError}
              error={error}
              topK={topK}
            />
          </div>
        </div>
      </Main>
    </>
  )
}
