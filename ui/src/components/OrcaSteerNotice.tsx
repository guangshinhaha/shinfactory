import { useOpenInOrca } from "~/client";
import type { Session, Snapshot } from "~/client";

// Orca workers can't take a steer from the dashboard; the factory refuses it.
export function isSteerable(session: Session | undefined): boolean {
  return session?.runtime !== "orca";
}

export function dispatchIDForSession(snapshot: Snapshot, sessionId: string): string | undefined {
  for (const order of snapshot.orders) {
    const stage = order.stages.find((s) => s.session_id === sessionId && s.dispatch_id);
    if (stage) {
      return stage.dispatch_id;
    }
  }
  return undefined;
}

export function OrcaSteerNotice({ dispatchID }: { dispatchID: string | undefined }) {
  const open = useOpenInOrca();
  return (
    <div className="input-area">
      <div className="input-label">This worker runs in Orca and can't be steered from here.</div>
      <div className="input-row">
        <button
          type="button"
          className="btn-submit"
          disabled={!dispatchID || open.isPending}
          onClick={() => dispatchID && open.mutate(dispatchID)}
        >
          Open in Orca
        </button>
        {open.isError && (
          <span className="stage-orca-error" role="alert">
            {open.error.message}
          </span>
        )}
      </div>
    </div>
  );
}
