// The one Node API vite.config.ts uses; the project has no Node types.
declare module "node:fs" {
  export function readFileSync(path: URL, encoding: "utf8"): string;
}
