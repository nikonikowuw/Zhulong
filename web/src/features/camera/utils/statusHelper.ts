import type { HealthState, SessionState } from "../types";

export interface StatusBadgeConfig {
  variant: "positive" | "danger" | "warning" | "muted" | "accent";
  dotColor: string;
  labelKey: string;
}

export function getHealthBadgeConfig(health: HealthState): StatusBadgeConfig {
  switch (health) {
    case "online":
      return {
        variant: "positive",
        dotColor: "var(--positive)",
        labelKey: "camera.health.online",
      };
    case "offline":
      return {
        variant: "muted",
        dotColor: "var(--muted)",
        labelKey: "camera.health.offline",
      };
    case "error":
      return {
        variant: "danger",
        dotColor: "var(--danger)",
        labelKey: "camera.health.error",
      };
    case "unknown":
    default:
      return {
        variant: "warning",
        dotColor: "#f5a623",
        labelKey: "camera.health.unknown",
      };
  }
}

export function getSessionBadgeConfig(session: SessionState): StatusBadgeConfig {
  switch (session) {
    case "running":
      return {
        variant: "accent",
        dotColor: "var(--accent)",
        labelKey: "camera.session.running",
      };
    case "starting":
      return {
        variant: "warning",
        dotColor: "#f5a623",
        labelKey: "camera.session.starting",
      };
    case "reconnecting":
      return {
        variant: "warning",
        dotColor: "#f5a623",
        labelKey: "camera.session.reconnecting",
      };
    case "error":
      return {
        variant: "danger",
        dotColor: "var(--danger)",
        labelKey: "camera.session.error",
      };
    case "idle":
    default:
      return {
        variant: "muted",
        dotColor: "var(--muted)",
        labelKey: "camera.session.idle",
      };
  }
}
