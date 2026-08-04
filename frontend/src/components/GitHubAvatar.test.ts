import { describe, expect, it, vi } from "vitest";
import { actorAvatarURL, createGitHubAvatarLoader } from "./GitHubAvatar";

describe("GitHub avatar loading", () => {
  it("coalesces concurrent and later requests for the same login", async () => {
    const fetchAvatar = vi.fn(async () => ({
      ok: true,
      blob: async () => new Blob(["avatar"]),
    }));
    const createObjectURL = vi.fn(() => "blob:shared-avatar");
    const loader = createGitHubAvatarLoader(fetchAvatar, createObjectURL);
    const first = { login: "Viking602", name: "Viking", avatarUrl: "https://avatars.githubusercontent.com/Viking602?size=64" };
    const duplicate = { login: "viking602", name: "Viking", avatarUrl: "https://avatars.githubusercontent.com/u/27732503?s=64" };

    const [firstSource, duplicateSource] = await Promise.all([loader.load(first), loader.load(duplicate)]);
    const laterSource = await loader.load(first);

    expect(fetchAvatar).toHaveBeenCalledTimes(1);
    expect(createObjectURL).toHaveBeenCalledTimes(1);
    expect(firstSource).toBe("blob:shared-avatar");
    expect(duplicateSource).toBe(firstSource);
    expect(laterSource).toBe(firstSource);
    expect(loader.peek(duplicate)).toBe(firstSource);
  });

  it("keeps different users isolated and skips actors without an identity", async () => {
    const fetchAvatar = vi.fn(async () => ({
      ok: true,
      blob: async () => new Blob(["avatar"]),
    }));
    const loader = createGitHubAvatarLoader(fetchAvatar, (_blob) => `blob:avatar-${fetchAvatar.mock.calls.length}`);

    await loader.load({ login: "octocat" });
    await loader.load({ login: "hubot" });
    expect(await loader.load({ login: "" })).toBeNull();

    expect(fetchAvatar).toHaveBeenCalledTimes(2);
    expect(actorAvatarURL({ login: "octocat" })).toBe("https://avatars.githubusercontent.com/octocat?size=64");
  });

  it("does not retry a failed avatar for every rendered occurrence", async () => {
    const fetchAvatar = vi.fn(async () => ({
      ok: false,
      blob: async () => new Blob(),
    }));
    const loader = createGitHubAvatarLoader(fetchAvatar, () => "blob:unused");
    const actor = { login: "offline-user" };

    expect(await loader.load(actor)).toBeNull();
    expect(await loader.load(actor)).toBeNull();
    expect(fetchAvatar).toHaveBeenCalledTimes(1);
  });
});
