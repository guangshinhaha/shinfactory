import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { TopBarView } from "./TopBar";
import { repoColor, repoFavicon, repoTitle } from "~/client/repo-identity";

vi.mock("~/client", () => ({ useConfig: vi.fn(), useSuspenseSnapshot: vi.fn() }));

const configs = [
  { repo: "gantry", port: "3000", buildSha: "abc1234", loopState: "running" },
  { repo: "shinfactory", port: "3001", buildSha: "def5678", loopState: "paused" },
];

describe("two loops are told apart", () => {
  it("renders repo, port, build and loop state in the top bar", () => {
    for (const cfg of configs) {
      const { unmount } = render(<TopBarView {...cfg} />);
      expect(screen.getByTestId("top-bar-repo").textContent).toBe(cfg.repo);
      expect(screen.getByTestId("top-bar-port").textContent).toBe(`:${cfg.port}`);
      expect(screen.getByTestId("top-bar-sha").textContent).toBe(cfg.buildSha);
      expect(screen.getByTestId("top-bar-state").textContent).toBe(`loop ${cfg.loopState}`);
      unmount();
    }
  });

  it("gives each repo its own title, colour and favicon, stable across calls", () => {
    const [a, b] = configs;
    expect(repoTitle(a.repo)).toBe("shinfactory · gantry");
    expect(repoTitle(b.repo)).toBe("shinfactory · shinfactory");
    expect(repoColor(a.repo)).toBe(repoColor(a.repo));
    expect(repoColor(a.repo)).not.toBe(repoColor(b.repo));
    expect(repoFavicon(a.repo)).not.toBe(repoFavicon(b.repo));
    expect(decodeURIComponent(repoFavicon(a.repo))).toContain(repoColor(a.repo));
  });
});
