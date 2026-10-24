import fc from "fast-check";

const seed = Number(process.env.ASSURE_SEED);
if (Number.isInteger(seed)) {
  fc.configureGlobal({ ...fc.readConfigureGlobal(), seed });
}
