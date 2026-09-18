import assert from "node:assert/strict";
import test from "node:test";

import { mailboxIsRouted, mailboxRoutingSummary } from "../src/mailboxRouting.js";

test("mailbox without recipient domains is explicitly not routed", () => {
  assert.equal(mailboxIsRouted("  \n"), false);
  assert.equal(mailboxRoutingSummary("  \n"), "Not routed");
});

test("mailbox routing summary normalizes comma and newline separated domains", () => {
  assert.equal(mailboxIsRouted("alpha.test"), true);
  assert.equal(mailboxRoutingSummary("alpha.test,\n beta.test"), "Routed · alpha.test, beta.test");
});
