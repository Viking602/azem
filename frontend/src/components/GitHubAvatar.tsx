import { useEffect, useState } from "react";
import type { PullRequestActor } from "../types";

type AvatarIdentity = Pick<PullRequestActor, "login" | "name" | "avatarUrl">;
type AvatarResponse = Pick<Response, "ok" | "blob">;
type AvatarFetcher = (url: string, init: RequestInit) => Promise<AvatarResponse>;
type AvatarEntry = { source: string | null; promise: Promise<string | null> };

export interface GitHubAvatarLoader {
  load: (actor: AvatarIdentity) => Promise<string | null>;
  peek: (actor: AvatarIdentity) => string | null;
}

export function actorAvatarURL(actor: AvatarIdentity): string {
  const explicit = actor.avatarUrl?.trim();
  if (explicit) return explicit;
  const login = actor.login.trim();
  return login ? `https://avatars.githubusercontent.com/${encodeURIComponent(login)}?size=64` : "";
}

function avatarCacheKey(actor: AvatarIdentity, requestURL: string): string {
  const login = actor.login.trim().toLowerCase();
  return login ? `github:${login}` : `url:${requestURL}`;
}

export function createGitHubAvatarLoader(
  fetchAvatar: AvatarFetcher = (url, init) => fetch(url, init),
  createObjectURL: (blob: Blob) => string = (blob) => URL.createObjectURL(blob),
): GitHubAvatarLoader {
  const entries = new Map<string, AvatarEntry>();
  const entryFor = (actor: AvatarIdentity) => {
    const requestURL = actorAvatarURL(actor);
    return requestURL ? entries.get(avatarCacheKey(actor, requestURL)) : undefined;
  };

  return {
    peek: (actor) => entryFor(actor)?.source ?? null,
    load: (actor) => {
      const requestURL = actorAvatarURL(actor);
      if (!requestURL) return Promise.resolve(null);
      const key = avatarCacheKey(actor, requestURL);
      const cached = entries.get(key);
      if (cached) return cached.promise;

      const entry: AvatarEntry = { source: null, promise: Promise.resolve(null) };
      entry.promise = fetchAvatar(requestURL, {
        cache: "force-cache",
        credentials: "omit",
        referrerPolicy: "no-referrer",
      })
        .then((response) => response.ok ? response.blob() : Promise.reject(new Error("avatar request failed")))
        .then((blob) => createObjectURL(blob))
        .catch(() => null)
        .then((source) => {
          entry.source = source;
          return source;
        });
      entries.set(key, entry);
      return entry.promise;
    },
  };
}

const sharedAvatarLoader = createGitHubAvatarLoader();

export default function GitHubAvatar({ actor }: { actor: AvatarIdentity }) {
  const label = actor.name || actor.login || "GitHub";
  const [source, setSource] = useState(() => sharedAvatarLoader.peek(actor));
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let active = true;
    setFailed(false);
    setSource(sharedAvatarLoader.peek(actor));
    void sharedAvatarLoader.load(actor).then((next) => {
      if (active) setSource(next);
    });
    return () => { active = false; };
  }, [actor.avatarUrl, actor.login]);

  if (source && !failed) {
    return <img className="pull-request-avatar" src={source} alt="" aria-hidden="true" draggable={false} onError={() => setFailed(true)} />;
  }
  return <span className="pull-request-avatar" aria-hidden="true">{label.slice(0, 1).toUpperCase()}</span>;
}
