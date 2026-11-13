import { useCallback, useState } from 'react'
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
import { LOWER_RETRY_THRESHOLD } from './data/match-utils'
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
    appliedThreshold,
    appliedTopK,
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
  const [inspectPersonId, setInspectPersonId] = useState<string | null>(null)

  // 从当前结果列表纯渲染派生选中项与排名，杜绝换图/重搜后残留过期候选快照
  const inspectedIndex =
    inspectPersonId && results
      ? results.findIndex((item) => item.personId === inspectPersonId)
      : -1
  const inspectedMatch =
    inspectedIndex >= 0 && results ? results[inspectedIndex] : null
  const inspectedRank = inspectedIndex >= 0 ? inspectedIndex + 1 : 1

  const handleFileSelect = useCallback(
    (file: File | null) => {
      setInspectPersonId(null)
      setFile(file)
    },
    [setFile]
  )

  const handleReset = useCallback(() => {
    setInspectPersonId(null)
    reset()
  }, [reset])

  const handleSearch = useCallback(() => {
    setInspectPersonId(null)
    search()
  }, [search])

  const handleLowerThresholdAndSearch = useCallback(() => {
    setInspectPersonId(null)
    search({ threshold: LOWER_RETRY_THRESHOLD })
  }, [search])

  const handleInspect = useCallback((personId: string) => {
    setInspectPersonId(personId)
  }, [])

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
                className='hidden items-center gap-1 border-primary/30 bg-primary/10 px-2 py-0.5 font-mono text-[11px] font-semibold text-primary sm:inline-flex dark:border-primary/40 dark:bg-primary/20'
              >
                <Sparkles className='h-3 w-3' />
                <span>{t('engineBadge')}</span>
              </Badge>
            </div>
            <p className='text-xs text-muted-foreground sm:text-sm'>
              {t('subtitle')}
            </p>
          </div>
        </div>

        {/* 主工作区分割：左侧原图与检索控制，右侧视觉相册网格/列表 */}
        <div className='grid grid-cols-1 items-stretch gap-6 lg:grid-cols-12'>
          {/* 左侧控制面板列（大屏保持粘性） */}
          <div className='lg:col-span-4 xl:col-span-3'>
            <div className='flex flex-col gap-4 lg:sticky lg:top-20'>
              <ImageSearchDropzone
                selectedFile={selectedFile}
                previewUrl={previewUrl}
                onFileSelect={handleFileSelect}
                disabled={isSearching}
                pasteSuspended={inspectedMatch !== null}
              />

              <ImageSearchControls
                threshold={threshold}
                onThresholdChange={setThreshold}
                topK={topK}
                onTopKChange={setTopK}
                onSearch={handleSearch}
                onReset={handleReset}
                canSearch={!!selectedFile}
                isSearching={isSearching}
                uploadProgress={uploadProgress}
              />
            </div>
          </div>

          {/* 检索结果展示面板 */}
          <div className='flex h-full flex-1 flex-col lg:col-span-8 xl:col-span-9'>
            <ImageSearchResults
              results={results}
              isSearching={isSearching}
              isError={isError}
              error={error}
              appliedThreshold={appliedThreshold}
              appliedTopK={appliedTopK ?? topK}
              viewMode={viewMode}
              onViewModeChange={setViewMode}
              onInspect={handleInspect}
              onRetry={handleSearch}
              onLowerThresholdAndSearch={handleLowerThresholdAndSearch}
            />
          </div>
        </div>
      </Main>

      {/* 1:1 双图原图 vs 底库五官比对弹窗 */}
      <ImageSearchCompareDialog
        match={inspectedMatch}
        rank={inspectedRank}
        previewUrl={previewUrl}
        open={inspectedMatch !== null}
        onOpenChange={(open) => {
          if (!open) setInspectPersonId(null)
        }}
      />
    </>
  )
}
