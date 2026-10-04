import { createContext } from "react";
import type {
  AuthPhase,
  InitAdminPayload,
  LoginPayload,
  User,
} from "../types";

export interface AuthContextValue {
  phase: AuthPhase;
  user: User | null;
  error: string | null;
  clearError: () => void;
  initAdmin: (payload: InitAdminPayload) => Promise<void>;
  login: (payload: LoginPayload) => Promise<void>;
  logout: () => Promise<void>;
  checkAuth: () => Promise<void>;
}

export const AuthContext = createContext<AuthContextValue | null>(null);
