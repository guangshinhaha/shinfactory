import { useEffect } from "react";
import { useConfig, useSendControl, useSuspenseSnapshot, useWSStatus } from "~/client";
import type { ControlAck, ControlAction, LoopState, Snapshot } from "~/client";

const STATE_CLASS: Record<LoopState, string> = {
  running: "bg-accent text-bg-depth border-accent",
  paused: "bg-yellow-glow text-yellow border-yellow",
  draining: "bg-yellow-glow text-yellow border-yellow",
  idle: "bg-transparent text-text-secondary border-border-active",
};

export function needsYouCount(snapshot: Snapshot): number {
  return snapshot.pending_review_count + snapshot.action_needed.length;
}

export function tabTitle(repo: string, snapshot: Snapshot): string {
  return [
    repo || "shinfactory",
    `${snapshot.active.length} running`,
    `${needsYouCount(snapshot)} needs you`,
  ].join(" · ");
}

function ackMessage(ack: ControlAck | undefined, error: Error | null): string {
  if (error) {
    return `error: ${error.message}`;
  }
  if (!ack) {
    return "";
  }
  return `${ack.action}: ${ack.status}${ack.message ? ` — ${ack.message}` : ""}`;
}

export function TopBar() {
  const { data: snapshot } = useSuspenseSnapshot();
  const { data: config } = useConfig();
  const wsStatus = useWSStatus();
  const control = useSendControl();

  const repo = config?.repo ?? "";
  const title = tabTitle(repo, snapshot);
  useEffect(() => {
    document.title = title;
  }, [title]);

  const paused = snapshot.loop_state === "paused";

  function send(action: Extract<ControlAction, "pause" | "resume" | "drain" | "stop-all">) {
    // oxlint-disable-next-line no-alert -- Stop all is destructive; native confirm is the lightest gate
    if (action === "stop-all" && !globalThis.confirm("Stop all running sessions?")) {
      return;
    }
    control.mutate({ action });
  }

  const ack = control.data;
  const ackIsError = control.isError || ack?.status === "error";
  const ackText = ackMessage(ack, control.error);

  return (
    <header
      className="flex items-center gap-4 px-4 py-2 border-b border-border-subtle bg-bg-depth text-xs"
      aria-label="Loop status"
    >
      <span className="font-semibold">{repo || "shinfactory"}</span>
      <span className="text-text-secondary">port {globalThis.location.port || "80"}</span>
      <span
        className={`px-2.5 py-1 font-semibold border ${STATE_CLASS[snapshot.loop_state]}`}
        aria-label="Loop state"
      >
        {snapshot.loop_state}
      </span>
      <span aria-label="Mode">{snapshot.mode}</span>
      <span aria-label="Slots">
        {snapshot.active.length}/{snapshot.max_concurrency} slots
      </span>

      <span className="flex items-center gap-2">
        <button
          type="button"
          className="px-2 py-1 border border-border-active"
          disabled={control.isPending}
          onClick={() => send(paused ? "resume" : "pause")}
        >
          {paused ? "Resume" : "Pause"}
        </button>
        <button
          type="button"
          className="px-2 py-1 border border-border-active"
          disabled={control.isPending}
          onClick={() => send("drain")}
        >
          Drain
        </button>
        <button
          type="button"
          className="px-2 py-1 border border-red text-red"
          disabled={control.isPending}
          onClick={() => send("stop-all")}
        >
          Stop all
        </button>
      </span>
      <span
        role="status"
        aria-label="Control ack"
        className={ackIsError ? "text-red" : "text-text-secondary"}
      >
        {ackText}
      </span>

      <span
        role="status"
        aria-label="Connection"
        className={`ml-auto ${wsStatus === "connected" ? "text-green" : "text-red"}`}
      >
        connection: {wsStatus}
      </span>
    </header>
  );
}
