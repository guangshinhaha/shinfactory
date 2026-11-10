import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StageRail } from "./StageRail";
import { buildStage } from "../test-utils";

const mutate = vi.fn();

vi.mock("~/client", async () => {
  const actual = await vi.importActual("~/client");
  return {
    ...(actual as object),
    useOpenInOrca: () => ({ mutate, isPending: false, isError: false, error: null }),
  };
});

beforeEach(() => mutate.mockClear());

describe("StageRail", () => {
  it("shows model, dispatch id and an open-in-Orca action for an orca stage", async () => {
    render(
      <StageRail
        stages={[
          buildStage({
            task_key: "execute",
            model: "haiku",
            status: "active",
            dispatch_id: "ctx_8f03",
          }),
        ]}
      />,
    );
    expect(screen.getByText("haiku")).toBeTruthy();
    expect(screen.getByText("ctx_8f03")).toBeTruthy();
    await userEvent.click(screen.getByRole("button", { name: "open in Orca" }));
    expect(mutate).toHaveBeenCalledWith("ctx_8f03");
  });

  it("offers no Orca action for a process-runtime stage", () => {
    render(<StageRail stages={[buildStage({ task_key: "execute", status: "active" })]} />);
    expect(screen.queryByRole("button", { name: "open in Orca" })).toBeNull();
  });
});
