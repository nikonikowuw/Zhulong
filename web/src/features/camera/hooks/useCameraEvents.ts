import { useEffect, useRef, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { CAMERAS_QUERY_KEY } from "./useCameras";
import type { CameraResponse, CameraStateInfo } from "../types";

export type SSEConnectionState = "connected" | "connecting" | "disconnected";

function mergeStateIntoCamera(camera: CameraResponse, state: CameraStateInfo): CameraResponse {
  const updatedStreams = camera.streams.map((stream) => {
    const streamState = state.streams?.[stream.role];
    if (!streamState) return stream;
    return {
      ...stream,
      runtimeState: streamState,
    };
  });

  return {
    ...camera,
    enabled: state.enabled,
    revision: state.revision,
    health: state.health,
    session: state.session,
    degraded: state.degraded ?? camera.degraded,
    stale: state.stale ?? camera.stale,
    reason: state.reason ?? camera.reason,
    lastCheckedAt: state.lastCheckedAt ?? camera.lastCheckedAt,
    lastSuccessAt: state.lastSuccessAt ?? camera.lastSuccessAt,
    streams: updatedStreams,
  };
}

export function useCameraEvents(enabled = true) {
  const queryClient = useQueryClient();
  const [connectionState, setConnectionState] = useState<SSEConnectionState>("disconnected");
  const eventSourceRef = useRef<EventSource | null>(null);
  const reconnectTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const backoffRef = useRef(1000);

  useEffect(() => {
    if (!enabled || typeof EventSource === "undefined") {
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
      return;
    }

    let isSubscribed = true;

    function connect() {
      if (!isSubscribed) return;

      const es = new EventSource("/api/v1/cameras/events", { withCredentials: true });
      eventSourceRef.current = es;

      es.onopen = () => {
        if (!isSubscribed) return;
        setConnectionState("connected");
        backoffRef.current = 1000; // Reset backoff
      };

      es.addEventListener("snapshot", (event: MessageEvent) => {
        if (!isSubscribed) return;
        try {
          const snapshotList: CameraStateInfo[] = JSON.parse(event.data);
          const stateMap = new Map<string, CameraStateInfo>();
          for (const item of snapshotList) {
            stateMap.set(item.cameraId, item);
          }

          queryClient.setQueriesData<CameraResponse[]>({ queryKey: CAMERAS_QUERY_KEY }, (old) => {
            if (!old) return old;
            return old.map((cam) => {
              const matching = stateMap.get(cam.id);
              return matching ? mergeStateIntoCamera(cam, matching) : cam;
            });
          });
        } catch {
          // Ignore JSON parse errors
        }
      });

      es.addEventListener("change", (event: MessageEvent) => {
        if (!isSubscribed) return;
        try {
          const changeItem: CameraStateInfo = JSON.parse(event.data);
          queryClient.setQueriesData<CameraResponse[]>({ queryKey: CAMERAS_QUERY_KEY }, (old) => {
            if (!old) return old;
            return old.map((cam) => (cam.id === changeItem.cameraId ? mergeStateIntoCamera(cam, changeItem) : cam));
          });
        } catch {
          // Ignore JSON parse errors
        }
      });

      es.onerror = () => {
        if (!isSubscribed) return;
        es.close();
        eventSourceRef.current = null;
        setConnectionState("disconnected");

        // Exponential backoff reconnect: 1s -> 2s -> 4s -> max 15s
        const nextDelay = Math.min(backoffRef.current, 15_000);
        backoffRef.current = nextDelay * 2;
        reconnectTimeoutRef.current = setTimeout(connect, nextDelay);
      };
    }

    connect();

    return () => {
      isSubscribed = false;
      if (reconnectTimeoutRef.current) {
        clearTimeout(reconnectTimeoutRef.current);
      }
      if (eventSourceRef.current) {
        eventSourceRef.current.close();
        eventSourceRef.current = null;
      }
    };
  }, [enabled, queryClient]);

  return { connectionState };
}
