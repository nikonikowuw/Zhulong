import { z } from "zod";
import type { SupportedLanguage } from "@/shared/i18n";
import { getApiData } from "@/shared/api/client";

export const healthSchema = z.object({
  status: z.literal("ready"),
  components: z.object({
    database: z.literal("ready"),
    engine: z.literal("ready"),
  }),
});

export type Health = z.infer<typeof healthSchema>;

export function getHealth(language: SupportedLanguage, signal?: AbortSignal): Promise<Health> {
  return getApiData("/api/v1/health", healthSchema, language, signal);
}
