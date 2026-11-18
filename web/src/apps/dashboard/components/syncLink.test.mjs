// Run with `npm test` (node --test; Node strips the types from syncLink.ts).
import assert from "node:assert/strict";
import { test } from "node:test";
import { syncDeepLink } from "./syncLink.ts";

test("the address is the one parameter, percent-encoded", () => {
  const address = "https://memd.example.com/sync/abcdefghijklmnopqrstuvwxyz234567";
  const link = syncDeepLink(address);
  assert.equal(
    link,
    "project-b://sync?address=https%3A%2F%2Fmemd.example.com%2Fsync%2Fabcdefghijklmnopqrstuvwxyz234567",
  );
  const url = new URL(link);
  assert.equal(url.protocol, "project-b:");
  assert.deepEqual([...url.searchParams.keys()], ["address"]);
  assert.equal(url.searchParams.get("address"), address);
});

test("characters that would split the query are encoded", () => {
  const link = syncDeepLink("http://127.0.0.1:7878/sync/a?b=c&d#e f");
  assert.equal(link, "project-b://sync?address=http%3A%2F%2F127.0.0.1%3A7878%2Fsync%2Fa%3Fb%3Dc%26d%23e%20f");
  assert.equal(new URL(link).searchParams.get("address"), "http://127.0.0.1:7878/sync/a?b=c&d#e f");
});
