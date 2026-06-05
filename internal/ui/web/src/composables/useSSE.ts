import { ref, type Ref, onUnmounted, getCurrentInstance } from 'vue';

export interface ProgressEvent {
  stage?: string;
  downloaded?: number;
  total?: number;
  speed_bps?: number;
  msg?: string;
}

export interface SSEHandle {
  events: Ref<ProgressEvent[]>;
  latest: Ref<ProgressEvent | undefined>;
  done: Ref<boolean>;
  error: Ref<string | null>;
  close(): void;
}

export function useSSE(streamId: string): SSEHandle {
  const events = ref<ProgressEvent[]>([]);
  const latest = ref<ProgressEvent | undefined>();
  const done = ref(false);
  const error = ref<string | null>(null);

  const es = new EventSource(`/api/events?stream=${encodeURIComponent(streamId)}`);

  es.onmessage = (e) => {
    try {
      const ev = JSON.parse(e.data) as ProgressEvent;
      events.value.push(ev);
      latest.value = ev;
    } catch (err) {
      error.value = '解析事件失败: ' + (err instanceof Error ? err.message : String(err));
    }
  };

  // SSE convention: server closes the connection when stream ends.
  // EventSource interprets that as an error and tries to reconnect; we
  // shut it down explicitly to mean "done".
  es.onerror = () => {
    if (es.readyState === EventSource.CLOSED) {
      done.value = true;
    } else {
      // Connection lost mid-stream; treat as terminal so caller stops waiting.
      es.close();
      done.value = true;
    }
  };

  function close() {
    es.close();
    done.value = true;
  }

  if (getCurrentInstance()) {
    onUnmounted(close);
  }

  return { events, latest, done, error, close };
}
