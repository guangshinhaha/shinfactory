import { useEffect } from "react";
import { useConfig, useSuspenseSnapshot } from "~/client";
import { repoColor, repoFavicon, repoTitle } from "~/client/repo-identity";

interface TopBarViewProps {
  repo: string;
  port: string;
  buildSha: string;
  loopState: string;
}

export function TopBarView({ repo, port, buildSha, loopState }: TopBarViewProps) {
  return (
    <header
      role="banner"
      aria-label={`Repo ${repo}`}
      className="flex items-center gap-4 px-4 py-1.5 text-white text-sm font-semibold"
      style={{ background: repoColor(repo) }}
    >
      <span data-testid="top-bar-repo">{repo}</span>
      <span data-testid="top-bar-port">:{port}</span>
      <span data-testid="top-bar-sha" className="font-mono font-normal opacity-80">
        {buildSha}
      </span>
      <span data-testid="top-bar-state" className="ml-auto">
        loop {loopState}
      </span>
    </header>
  );
}

function useRepoIdentity(repo: string) {
  useEffect(() => {
    document.title = repoTitle(repo);
    let link = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
    if (!link) {
      link = document.createElement("link");
      link.rel = "icon";
      document.head.appendChild(link);
    }
    link.type = "image/svg+xml";
    link.href = repoFavicon(repo);
  }, [repo]);
}

// Sets the tab title and favicon from the repo name. Rendered on every page,
// onboarding included.
export function RepoIdentity() {
  const { data } = useConfig();
  useRepoIdentity(data?.repo ?? "");
  return null;
}

export function TopBar() {
  const { data: config } = useConfig();
  const { data: snapshot } = useSuspenseSnapshot();
  return (
    <TopBarView
      repo={config?.repo ?? "…"}
      port={config?.port ?? ""}
      buildSha={config?.build_sha ?? ""}
      loopState={snapshot.loop_state}
    />
  );
}
