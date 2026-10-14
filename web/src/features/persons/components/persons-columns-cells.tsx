import { Image as ImageIcon } from 'lucide-react'
import { useTranslation } from 'react-i18next'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { type Person } from '../data/schema'
import { usePersonsContext } from './persons-provider'

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
