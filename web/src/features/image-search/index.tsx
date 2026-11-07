import { useState } from 'react'
import { Sparkles } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { ConfigDrawer } from '@/components/config-drawer'
import { LanguageSwitch } from '@/components/language-switch'
import { Header } from '@/components/layout/header'
import { Main } from '@/components/layout/main'
import { ProfileDropdown } from '@/components/profile-dropdown'
import { Search } from '@/components/search'
import { ThemeSwitch } from '@/components/theme-switch'
import { ImageSearchCompareDialog } from './components/image-search-compare-dialog'
import { ImageSearchControls } from './components/image-search-controls'
import { ImageSearchDropzone } from './components/image-search-dropzone'
import { ImageSearchResults } from './components/image-search-results'
import { type ImageSearchMatchInfo } from './data/schema'
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

  const [viewMode, setViewMode] = useState<'grid' | 'list'>('grid')
  const [inspectTarget, setInspectTarget] = useState<{
    match: ImageSearchMatchInfo
    rank: number
  } | null>(null)

  const handleInspect = (match: ImageSearchMatchInfo, rank: number) => {
    setInspectTarget({ match, rank })
  }

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

        {/* 主工作区分割：左侧原图与检索控制，右侧视觉相册网格/列表 */}
        <div className='grid grid-cols-1 items-stretch gap-6 lg:grid-cols-12'>
          {/* 左侧控制面板列（优化宽度比例，大屏保持粘性） */}
          <div className='lg:col-span-4 xl:col-span-3'>
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

          {/* 检索结果展示面板（大面积展示人脸相册网格） */}
          <div className='flex h-full flex-1 flex-col lg:col-span-8 xl:col-span-9'>
            <ImageSearchResults
              results={results}
              isSearching={isSearching}
              isError={isError}
              error={error}
              topK={topK}
              threshold={threshold}
              viewMode={viewMode}
              onViewModeChange={setViewMode}
              onInspect={handleInspect}
              onLowerThresholdAndSearch={() => {
                setThreshold(0.5)
                search()
              }}
            />
          </div>
        </div>
      </Main>

      {/* 1:1 双图原图 vs 底库五官比对弹窗 */}
      <ImageSearchCompareDialog
        match={inspectTarget?.match ?? null}
        rank={inspectTarget?.rank ?? 1}
        previewUrl={previewUrl}
        open={inspectTarget !== null}
        onOpenChange={(open) => {
          if (!open) setInspectTarget(null)
        }}
      />
    </>
  )
}
