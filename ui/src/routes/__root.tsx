import { Suspense, useMemo, useCallback, useEffect } from "react";
import { createRootRoute, Outlet, useLocation, useNavigate } from "@tanstack/react-router";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { ActiveChannelProvider } from "~/client";
import { connectWS } from "~/client/ws";
import type { ChannelId } from "~/client";
import { Sidebar } from "~/components/Sidebar";
import { RepoIdentity, TopBar } from "~/components/TopBar";
import "~/app.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      staleTime: Infinity,
      refetchOnWindowFocus: false,
    },
  },
});

function RootComponent() {
  const { pathname } = useLocation();
  const navigate = useNavigate();

  const activeChannel: ChannelId = useMemo(() => {
    if (pathname.startsWith("/actor/")) {
      return { type: "agent", sessionId: pathname.slice("/actor/".length) };
    }
    return { type: "scheduler" };
  }, [pathname]);

  const setActiveChannel = useCallback(
    (channel: ChannelId) => {
      if (channel.type === "scheduler") {
        navigate({ to: "/" });
      } else {
        navigate({ to: "/actor/$id", params: { id: channel.sessionId } });
      }
    },
    [navigate],
  );

  if (pathname === "/onboarding") {
    return (
      <>
        <RepoIdentity />
        <Outlet />
      </>
    );
  }

  return (
    <ActiveChannelProvider channel={activeChannel} onChannelChange={setActiveChannel}>
      <WSConnectionBridge />
      <RepoIdentity />
      <Suspense fallback={<div className="h-screen bg-bg-depth" />}>
        <div className="flex flex-col h-screen">
          <TopBar />
          <div className="app-layout flex-1 min-h-0" style={{ height: "auto" }}>
            <Sidebar />
            <Outlet />
          </div>
        </div>
      </Suspense>
    </ActiveChannelProvider>
  );
}

function WSConnectionBridge() {
  const qc = useQueryClient();
  useEffect(() => connectWS(qc), [qc]);
  return null;
}

export const Route = createRootRoute({
  component: () => (
    <QueryClientProvider client={queryClient}>
      <RootComponent />
    </QueryClientProvider>
  ),
});
