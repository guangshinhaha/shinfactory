export function repoColor(repo: string): string {
  let hash = 0;
  for (const ch of repo) {
    hash = (hash * 31 + ch.charCodeAt(0)) >>> 0;
  }
  return `hsl(${hash % 360} 65% 45%)`;
}

export function repoTitle(repo: string): string {
  return repo ? `shinfactory · ${repo}` : "shinfactory";
}

export function repoFavicon(repo: string): string {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><circle cx="16" cy="16" r="14" fill="${repoColor(repo)}"/></svg>`;
  return `data:image/svg+xml,${encodeURIComponent(svg)}`;
}
