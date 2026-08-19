import { describe, expect, it } from "vitest";
import { applies } from "./components/Fields";

/**
 * When a field applies.
 *
 * The condition decides both what is drawn and what is sent, so it is worth
 * being able to check it in a millisecond. The case that started it: a lamp
 * being switched off has no colour to choose.
 */
describe("a field that does not apply", () => {
  it("is out while the lights are being switched off", () => {
    expect(applies("power!=off", { power: "off" })).toBe(false);
    expect(applies("power!=off", { power: "on" })).toBe(true);
    expect(applies("power!=off", {})).toBe(true);
  });

  it("understands an empty one, which is how a choice replaces a typed address", () => {
    expect(applies("!account", {})).toBe(true);
    expect(applies("!account", { account: "" })).toBe(true);
    expect(applies("!account", { account: [] })).toBe(true);
    expect(applies("!account", { account: "the-bed" })).toBe(false);
  });

  it("takes one of several", () => {
    expect(applies("power=on,toggle", { power: "toggle" })).toBe(true);
    expect(applies("power=on,toggle", { power: "off" })).toBe(false);
  });

  it("says yes when nothing was asked", () => {
    expect(applies(undefined, {})).toBe(true);
    expect(applies("", { anything: 1 })).toBe(true);
  });

  it("counts a list with something in it as an answer", () => {
    expect(applies("host", { host: ["192.168.178.49"] })).toBe(true);
    expect(applies("host", { host: ["", ""] })).toBe(false);
  });
});
