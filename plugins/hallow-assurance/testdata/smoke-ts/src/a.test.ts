import fc from "fast-check";
import { expect, test } from "vitest";
import { double } from "./a";

test("double equals adding a number to itself", () => {
  fc.assert(
    fc.property(fc.integer(), (n) => {
      expect(double(n)).toBe(n + n);
    }),
  );
});

test("double distributes over addition", () => {
  fc.assert(
    fc.property(fc.integer({ min: -1e6, max: 1e6 }), fc.integer({ min: -1e6, max: 1e6 }), (a, b) => {
      expect(double(a + b)).toBe(double(a) + double(b));
    }),
  );
});
