import { AlertTriangle, Check, Cpu, Database, LoaderCircle, RefreshCw, Server } from "lucide-react";
import { useTranslation } from "react-i18next";
import { ApiError } from "@/shared/api/client";
import { useHealth } from "../hooks/useHealth";

export function HealthPanel() {
  const { data, error, isFetching, isPending, dataUpdatedAt, refetch } = useHealth();
  const { t, i18n } = useTranslation();

  if (isPending) {
    return (
      <section className="health-panel" aria-labelledby="health-title" aria-busy="true">
        <div className="panel-heading">
          <div>
            <h2 id="health-title">{t("health.title")}</h2>
          </div>
          <span className="panel-indicator is-checking">
            <LoaderCircle className="is-spinning" size={16} aria-hidden="true" />
            {t("health.checking")}
          </span>
        </div>
        <div className="health-message" role="status">
          <LoaderCircle className="health-state-icon is-spinning" size={22} aria-hidden="true" />
          <div>
            <h3>{t("health.loadingTitle")}</h3>
            <p>{t("health.loading")}</p>
          </div>
        </div>
        <div className="loading-rail" aria-hidden="true"><span /></div>
      </section>
    );
  }

  if (error) {
    const message = error instanceof ApiError && error.code !== "TIMEOUT"
      ? error.message
      : t("health.loadError");
    return (
      <section className="health-panel" aria-labelledby="health-title" aria-busy={isFetching}>
        <div className="panel-heading">
          <div>
            <h2 id="health-title">{t("health.title")}</h2>
          </div>
          <span className="panel-indicator is-unavailable">
            <AlertTriangle size={16} aria-hidden="true" />
            {t("health.errorTitle")}
          </span>
        </div>
        <div className="health-message is-error" role="alert">
          <AlertTriangle className="health-state-icon" size={22} aria-hidden="true" />
          <div>
            <h3>{t("health.errorTitle")}</h3>
            <p>{t("health.errorDescription", { message })}</p>
          </div>
        </div>
        <button className="action-button" type="button" onClick={() => void refetch()} disabled={isFetching}>
          <RefreshCw className={isFetching ? "is-spinning" : undefined} size={16} aria-hidden="true" />
          {t("health.retry")}
        </button>
      </section>
    );
  }

  if (!data) return null;
  const checkedAt = new Intl.DateTimeFormat(i18n.resolvedLanguage, {
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(dataUpdatedAt));

  return (
    <section className="health-panel" aria-labelledby="health-title" aria-busy={isFetching}>
      <div className="panel-heading">
        <div>
          <h2 id="health-title">{t("health.title")}</h2>
        </div>
        <div className="panel-tools">
          <span className="last-checked">{t("health.lastChecked", { time: checkedAt })}</span>
          <button
            className="icon-button"
            type="button"
            onClick={() => void refetch()}
            disabled={isFetching}
            aria-label={t("health.refresh")}
            title={t("health.refresh")}
          >
            <RefreshCw className={isFetching ? "is-spinning" : undefined} size={17} aria-hidden="true" />
          </button>
        </div>
      </div>
      <div className="health-message is-ready" role="status">
        <Check className="health-state-icon" size={22} aria-hidden="true" />
        <div>
          <h3>{t("health.readyTitle")}</h3>
          <p>{t("health.readyDescription")}</p>
        </div>
      </div>
      <ul className="component-grid" aria-label={t("health.title")}>
        <li className="component-row">
          <Server size={18} aria-hidden="true" />
          <div className="component-copy">
            <span>{t("components.host")}</span>
            <strong>{t("health.ready")}</strong>
          </div>
        </li>
        <li className="component-row">
          <Database size={18} aria-hidden="true" />
          <div className="component-copy">
            <span>{t("components.database")}</span>
            <strong>{t("health.ready")}</strong>
          </div>
        </li>
        <li className="component-row">
          <Cpu size={18} aria-hidden="true" />
          <div className="component-copy">
            <span>{t("components.engine")}</span>
            <strong>{t("health.ready")}</strong>
            <small>{t("components.engineNote")}</small>
          </div>
        </li>
      </ul>
    </section>
  );
}
