import { useOpenInOrca } from "~/client";
import type { Stage, StageStatus } from "~/client";

const dotClass: Record<StageStatus, string> = {
  completed: "done",
  active: "active",
  merging: "active",
  pending: "pending",
  failed: "failed",
  cancelled: "pending",
};

function OpenInOrca({ dispatchID }: { dispatchID: string }) {
  const open = useOpenInOrca();
  return (
    <span className="stage-orca">
      <span className="stage-dispatch" title="Orca dispatch id">
        {dispatchID}
      </span>
      <button
        type="button"
        className="stage-orca-open"
        disabled={open.isPending}
        onClick={() => open.mutate(dispatchID)}
      >
        open in Orca
      </button>
      {open.isError && (
        <span className="stage-orca-error" role="alert">
          {open.error.message}
        </span>
      )}
    </span>
  );
}

export function StageRail({ stages }: { stages: Stage[] }) {
  if (stages.length === 0) {
    return null;
  }

  return (
    <div className="stage-rail">
      {stages.map((stage, i) => {
        const isActive = stage.status === "active";
        const stageKey =
          stage.session_id ||
          stage.task_key ||
          `${stage.skill || "stage"}:${stage.provider}:${stage.model}:${stage.group ?? "none"}`;
        return (
          <div
            key={stageKey}
            className={`stage-item ${isActive ? "current" : ""}`}
            style={{
              animation: "fade-in 0.15s ease-out both",
              animationDelay: `${i * 30}ms`,
            }}
          >
            <div className={`stage-dot ${dotClass[stage.status] || "pending"}`} />
            <span>{stage.task_key || stage.skill || `Stage ${i + 1}`}</span>
            {stage.model && <span className="stage-model">{stage.model}</span>}
            {stage.dispatch_id && <OpenInOrca dispatchID={stage.dispatch_id} />}
          </div>
        );
      })}
    </div>
  );
}
