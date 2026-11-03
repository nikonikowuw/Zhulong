import { Eye, Image as ImageIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Avatar, AvatarFallback, AvatarImage } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { personsApi } from '../api/persons-api'
import { type Person } from '../data/schema'
import { usePersonsContext } from './persons-provider'

export function PersonAvatarCell({ person }: { person: Person }) {
  const { setPreviewImage } = usePersonsContext()
  const primaryFaceId = person.primaryFaceId || person.faces?.[0]?.id
  const avatarUrl = primaryFaceId
    ? personsApi.getFaceImageUrl(person.id, primaryFaceId)
    : undefined

  const fallbackChar = person.name
    ? person.name.trim().charAt(0).toUpperCase()
    : '?'

  const handlePreview = () => {
    if (!avatarUrl) return
    setPreviewImage({
      url: avatarUrl,
      title: `${person.name} (${primaryFaceId})`,
    })
  }

  if (!avatarUrl) {
    return (
      <div className='flex items-center justify-center'>
        <Avatar className='h-9 w-9 rounded-full border border-border bg-muted'>
          <AvatarFallback className='bg-primary/10 text-xs font-semibold text-primary'>
            {fallbackChar}
          </AvatarFallback>
        </Avatar>
      </div>
    )
  }

  return (
    <div className='flex items-center justify-center'>
      <button
        type='button'
        onClick={handlePreview}
        title={person.name}
        className='group relative flex shrink-0 cursor-pointer rounded-full focus:ring-2 focus:ring-primary focus:ring-offset-2 focus:outline-hidden'
      >
        <Avatar className='h-9 w-9 rounded-full border border-border shadow-xs transition-transform duration-200 group-hover:scale-110'>
          <AvatarImage
            src={avatarUrl}
            alt={person.name}
            className='object-cover'
          />
          <AvatarFallback className='bg-primary/10 text-xs font-semibold text-primary'>
            {fallbackChar}
          </AvatarFallback>
        </Avatar>
        <div className='absolute inset-0 flex items-center justify-center rounded-full bg-black/35 opacity-0 transition-opacity group-hover:opacity-100'>
          <Eye className='h-3.5 w-3.5 text-white drop-shadow-xs' />
        </div>
      </button>
    </div>
  )
}

export function PersonNameCell({ person }: { person: Person }) {
  return (
    <span className='truncate font-medium text-foreground'>{person.name}</span>
  )
}

export function PersonSourceCell({ person }: { person: Person }) {
  const { t } = useTranslation('persons')
  if (person.source === 'API') {
    return (
      <Badge
        variant='outline'
        className='gap-1 border-blue-500/30 bg-blue-500/10 text-[11px] text-blue-600 dark:text-blue-400'
        title={person.sourceName ? `API: ${person.sourceName}` : 'API'}
      >
        <span className='h-1.5 w-1.5 rounded-full bg-blue-500' />
        {person.sourceName ? person.sourceName : t('table.columns.sourceAPI')}
      </Badge>
    )
  }
  return (
    <Badge
      variant='secondary'
      className='text-[11px] font-normal text-muted-foreground'
    >
      {t('table.columns.sourceLocal')}
    </Badge>
  )
}

export function PersonsFacesCell({ person }: { person: Person }) {
  const { t } = useTranslation('persons')
  const { setOpen, setCurrentRow } = usePersonsContext()
  const faceCount = person.faces ? person.faces.length : 0

  const handleOpenFaces = () => {
    setCurrentRow(person)
    setOpen('faces')
  }

  return (
    <Button
      variant='outline'
      size='sm'
      onClick={handleOpenFaces}
      className='h-7 gap-1.5 px-2 text-xs font-normal hover:bg-muted'
    >
      <ImageIcon className='h-3.5 w-3.5 text-muted-foreground' />
      <span>{t('fields.faceCountUnit', { count: faceCount })}</span>
      {faceCount > 0 && (
        <Badge variant='secondary' className='h-4 px-1 text-[10px]'>
          {faceCount}
        </Badge>
      )}
    </Button>
  )
}
