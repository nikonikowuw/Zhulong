import { describe, expect, it } from "vitest";
import { z } from "zod";
import { createPaginatedSchema } from "./pagination";

describe("createPaginatedSchema", () => {
  const itemSchema = z.object({
    id: z.string(),
    name: z.string(),
  });
  const paginatedSchema = createPaginatedSchema(itemSchema);

  it("parses valid paginated response with defaults", () => {
    const raw = {
      items: [{ id: "1", name: "Camera 1" }],
      total: 1,
    };
    const res = paginatedSchema.safeParse(raw);
    expect(res.success).toBe(true);
    if (res.success) {
      expect(res.data.items).toHaveLength(1);
      expect(res.data.total).toBe(1);
      expect(res.data.page).toBe(1);
      expect(res.data.pageSize).toBe(20);
    }
  });

  it("parses explicit page, pageSize, and legacy limit, offset", () => {
    const raw = {
      items: [{ id: "1", name: "Camera 1" }],
      total: 50,
      page: 3,
      pageSize: 10,
      limit: 10,
      offset: 20,
    };
    const res = paginatedSchema.safeParse(raw);
    expect(res.success).toBe(true);
    if (res.success) {
      expect(res.data.page).toBe(3);
      expect(res.data.pageSize).toBe(10);
      expect(res.data.limit).toBe(10);
      expect(res.data.offset).toBe(20);
    }
  });

  it("rejects non-array items or invalid item types", () => {
    const raw = {
      items: "invalid",
      total: 10,
    };
    const res = paginatedSchema.safeParse(raw);
    expect(res.success).toBe(false);
  });
});
