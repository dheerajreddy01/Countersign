// The sign-in session: a cookie the server can check without storing it.
import { describe, expect, it } from "vitest";
import {
  SESSION_TTL_SECONDS,
  authConfig,
  checkCredentials,
  createSession,
  safeNext,
  verifySession,
} from "@/lib/session";

const cfg = authConfig({ CONSOLE_USER: "merchant", CONSOLE_PASSWORD: "s3cret:pw" })!;
const now = Date.UTC(2026, 10, 11);

describe("authConfig", () => {
  it("is off without both values", () => {
    expect(authConfig({ CONSOLE_USER: "merchant" })).toBeNull();
    expect(authConfig({ CONSOLE_PASSWORD: "x" })).toBeNull();
  });
});

describe("checkCredentials", () => {
  it("accepts only the exact pair", () => {
    expect(checkCredentials(cfg, "merchant", "s3cret:pw")).toBe(true);
    expect(checkCredentials(cfg, "merchant", "s3cret")).toBe(false);
    expect(checkCredentials(cfg, "Merchant", "s3cret:pw")).toBe(false);
  });
});

describe("sessions", () => {
  it("verifies a fresh token", async () => {
    expect(await verifySession(cfg, await createSession(cfg, now), now)).toBe(true);
  });

  it("rejects an expired token", async () => {
    const token = await createSession(cfg, now);
    expect(await verifySession(cfg, token, now + SESSION_TTL_SECONDS * 1000 + 1)).toBe(false);
  });

  it("rejects a token with a changed expiry", async () => {
    const [user, exp, mac] = (await createSession(cfg, now)).split(".");
    expect(await verifySession(cfg, `${user}.${Number(exp) + 3600}.${mac}`, now)).toBe(false);
  });

  it("is invalidated by a password change", async () => {
    const token = await createSession(cfg, now);
    const rotated = authConfig({ CONSOLE_USER: "merchant", CONSOLE_PASSWORD: "new" })!;
    expect(await verifySession(rotated, token, now)).toBe(false);
  });

  it("rejects garbage", async () => {
    expect(await verifySession(cfg, undefined, now)).toBe(false);
    expect(await verifySession(cfg, "a.b", now)).toBe(false);
    expect(await verifySession(cfg, "a.b.c", now)).toBe(false);
  });
});

describe("safeNext", () => {
  it("keeps same-site paths and refuses everything else", () => {
    expect(safeNext("/cases/7?tab=draft")).toBe("/cases/7?tab=draft");
    expect(safeNext("https://evil.example")).toBe("/");
    expect(safeNext("//evil.example")).toBe("/");
    expect(safeNext("/\\evil.example")).toBe("/");
    expect(safeNext("/login")).toBe("/");
    expect(safeNext(undefined)).toBe("/");
  });
});
