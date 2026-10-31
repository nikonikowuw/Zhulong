import { AlertTriangle, Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
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
        {/* 页面标题与引擎标识 */}
        <div className='flex flex-wrap items-center justify-between gap-3'>
          <div className='space-y-1'>
            <div className='flex items-center gap-2.5'>
              <h2 className='text-2xl font-bold tracking-tight text-foreground'>
                {t('title')}
              </h2>
              <Badge
                variant='outline'
                className='hidden items-center gap-1 border-primary/30 bg-primary/10 px-2 py-0.5 font-mono text-[11px] font-semibold text-primary sm:inline-flex'
              >
                <Sparkles className='h-3 w-3' />
                <span>1:N VECTOR</span>
              </Badge>
            </div>
            <p className='text-xs text-muted-foreground sm:text-sm'>
              {t('subtitle')}
            </p>
          </div>
        </div>

        {/* 人工核验与合规声明提示栏 */}
        <div className='relative flex items-center gap-3 overflow-hidden rounded-xl border border-amber-500/35 bg-gradient-to-r from-amber-500/15 via-amber-500/5 to-transparent px-3.5 py-2.5 text-xs text-amber-800 backdrop-blur-xs dark:text-amber-300'>
          <div className='flex h-6 w-6 shrink-0 items-center justify-center rounded-lg bg-amber-500/20 text-amber-600 dark:text-amber-400'>
            <AlertTriangle className='h-3.5 w-3.5' />
          </div>
          <span className='leading-relaxed font-medium'>{t('disclaimer')}</span>
        </div>

        {/* 主工作区分割：左侧上传与控制，右侧结果展示 */}
        <div className='grid grid-cols-1 items-stretch gap-6 lg:grid-cols-12'>
          {/* 左侧控制面板列 */}
          <div className='lg:col-span-5 xl:col-span-4'>
            <div className='flex flex-col gap-4 lg:sticky lg:top-20'>
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
          </div>

          {/* 检索结果展示面板（高度自适应与左侧严密平齐） */}
          <div className='flex h-full flex-1 flex-col lg:col-span-7 xl:col-span-8'>
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
