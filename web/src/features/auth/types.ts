import { z } from "zod";

export const authStatusSchema = z.object({
  initialized: z.boolean(),
});
export type AuthStatus = z.infer<typeof authStatusSchema>;

export const userSchema = z.object({
  id: z.number(),
  username: z.string(),
  createdAt: z.string().optional(),
});
export type User = z.infer<typeof userSchema>;

export type AuthPhase = "loading" | "uninitialized" | "unauthenticated" | "authenticated" | "error";

export interface InitAdminPayload {
  username: string;
  password: string;
  confirmPassword: string;
}

export interface LoginPayload {
  username: string;
  password: string;
}
