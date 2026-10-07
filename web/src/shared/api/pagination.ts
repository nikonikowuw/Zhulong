import { z } from "zod";

/**
 * Standard paginated response data schema factory.
 * Enforces items array, total count, page, and pageSize.
 * Also tolerates legacy limit and offset properties.
 */
export function createPaginatedSchema<T extends z.ZodTypeAny>(itemSchema: T) {
  return z.object({
    items: z.array(itemSchema),
    total: z.number(),
    page: z.number().default(1),
    pageSize: z.number().default(20),
    limit: z.number().optional(),
    offset: z.number().optional(),
  });
}

export type PaginatedData<T> = {
  items: T[];
  total: number;
  page: number;
  pageSize: number;
  limit?: number;
  offset?: number;
};
