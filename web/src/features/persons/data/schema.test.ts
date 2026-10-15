import { describe, expect, it } from 'vitest'
import {
  createPersonSchema,
  personListResponseSchema,
  personSchema,
  setPrimaryFaceSchema,
  updatePersonSchema,
} from './schema'

describe('person schema validation', () => {
  describe('personSchema', () => {
    it('parses valid person entity with empty faces array default', () => {
      const raw = {
        id: 'p-01',
        personId: 'EMP001',
        name: 'Alice',
        createdAt: '2026-10-14T00:00:00Z',
        updatedAt: '2026-10-14T00:00:00Z',
      }
      const parsed = personSchema.parse(raw)
      expect(parsed.id).toBe('p-01')
      expect(parsed.personId).toBe('EMP001')
      expect(parsed.name).toBe('Alice')
      expect(parsed.primaryFaceId).toBeUndefined()
      expect(parsed.faces).toEqual([])
    })

    it('parses person with null personId', () => {
      const raw = {
        id: 'p-01',
        personId: null,
        name: 'Alice',
        createdAt: '2026-10-14T00:00:00Z',
        updatedAt: '2026-10-14T00:00:00Z',
      }
      const parsed = personSchema.parse(raw)
      expect(parsed.personId).toBeNull()
    })

    it('parses person with primaryFaceId', () => {
      const raw = {
        id: 'p-01',
        personId: 'EMP001',
        name: 'Alice',
        primaryFaceId: 'f-01',
        createdAt: '2026-10-14T00:00:00Z',
        updatedAt: '2026-10-14T00:00:00Z',
      }
      const parsed = personSchema.parse(raw)
      expect(parsed.primaryFaceId).toBe('f-01')
    })

    it('parses person with populated faces', () => {
      const raw = {
        id: 'p-02',
        personId: 'EMP002',
        name: 'Bob',
        createdAt: '2026-10-14T00:00:00Z',
        updatedAt: '2026-10-14T00:00:00Z',
        faces: [
          {
            id: 'f-01',
            ownerRowId: 'p-02',
            imagePath: '/data/faces/bob.jpg',
            createdAt: '2026-10-14T01:00:00Z',
          },
        ],
      }
      const parsed = personSchema.parse(raw)
      expect(parsed.faces.length).toBe(1)
      expect(parsed.faces[0].imagePath).toBe('/data/faces/bob.jpg')
    })
  })

  describe('personListResponseSchema', () => {
    it('parses list envelope data structure', () => {
      const raw = {
        items: [
          {
            id: 'p-01',
            personId: 'EMP001',
            name: 'Alice',
            createdAt: '2026-10-14T00:00:00Z',
            updatedAt: '2026-10-14T00:00:00Z',
            faces: [],
          },
        ],
        total: 1,
        page: 1,
        pageSize: 10,
      }
      const parsed = personListResponseSchema.parse(raw)
      expect(parsed.total).toBe(1)
      expect(parsed.items.length).toBe(1)
    })
  })

  describe('createPersonSchema', () => {
    it('accepts valid name and optional personId', () => {
      const res = createPersonSchema.safeParse({
        name: 'Charlie',
        personId: 'EMP003',
      })
      expect(res.success).toBe(true)
    })

    it('accepts empty personId', () => {
      const res = createPersonSchema.safeParse({
        name: 'Charlie',
        personId: '',
      })
      expect(res.success).toBe(true)
    })

    it('rejects empty name', () => {
      const res = createPersonSchema.safeParse({
        name: '   ',
      })
      expect(res.success).toBe(false)
    })
  })

  describe('updatePersonSchema', () => {
    it('accepts valid updated name', () => {
      const res = updatePersonSchema.safeParse({
        name: 'Alice Cooper',
      })
      expect(res.success).toBe(true)
    })

    it('accepts valid personId for binding', () => {
      const res = updatePersonSchema.safeParse({
        name: 'Alice Cooper',
        personId: 'EMP_BOUND',
      })
      expect(res.success).toBe(true)
    })

    it('rejects empty updated name', () => {
      const res = updatePersonSchema.safeParse({
        name: '',
      })
      expect(res.success).toBe(false)
    })
  })

  describe('setPrimaryFaceSchema', () => {
    it('accepts non-empty faceId', () => {
      const res = setPrimaryFaceSchema.safeParse({ faceId: 'face_123' })
      expect(res.success).toBe(true)
    })

    it('rejects empty faceId', () => {
      const res = setPrimaryFaceSchema.safeParse({ faceId: '' })
      expect(res.success).toBe(false)
    })
  })
})
