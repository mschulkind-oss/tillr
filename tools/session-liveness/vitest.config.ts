import { fileURLToPath } from "node:url";

const fork = process.env.PI_FORK ?? fileURLToPath(new URL("../../.forks/pi", import.meta.url));
const repo = fileURLToPath(new URL("../..", import.meta.url));
const src = (path: string) => `${fork}/packages/${path}`;

export default {
	root: repo,
	resolve: {
		alias: [
			{ find: /^@earendil-works\/chord$/, replacement: src("chord/src/index.ts") },
			{ find: /^@earendil-works\/chord\/context$/, replacement: src("chord/src/context/index.ts") },
			{ find: /^@earendil-works\/chord\/delta$/, replacement: src("chord/src/delta/index.ts") },
			{ find: /^@earendil-works\/chord\/node$/, replacement: src("chord/src/node.ts") },
			{ find: /^@earendil-works\/pi-telemetry$/, replacement: src("telemetry/src/index.ts") },
			{ find: /^@earendil-works\/pi-telemetry\/testing$/, replacement: src("telemetry/src/testing/index.ts") },
			{ find: /^@earendil-works\/pi-mcp$/, replacement: src("mcp/src/index.ts") },
			{ find: /^@earendil-works\/pi-mcp\/oauth$/, replacement: src("mcp/src/oauth/index.ts") },
			{ find: /^@earendil-works\/pi-mcp\/testing$/, replacement: src("mcp/src/testing/index.ts") },
			{ find: /^@earendil-works\/pi-ai$/, replacement: src("ai/src/index.ts") },
			{ find: /^@earendil-works\/pi-ai\/compat$/, replacement: src("ai/src/compat.ts") },
			{ find: /^@earendil-works\/pi-ai\/oauth$/, replacement: src("ai/src/oauth.ts") },
			{ find: /^@earendil-works\/pi-ai\/utils\/(.+)$/, replacement: `${src("ai/src/utils")}/$1.ts` },
			{ find: /^@earendil-works\/pi-ai\/api\/(.+)$/, replacement: `${src("ai/src/api")}/$1.ts` },
			{ find: /^@earendil-works\/pi-ai\/providers\/(.+)$/, replacement: `${src("ai/src/providers")}/$1.ts` },
			{ find: /^@earendil-works\/pi-agent-core$/, replacement: src("agent/src/index.ts") },
			{ find: /^@earendil-works\/pi-codemode$/, replacement: src("codemode/src/index.ts") },
			{ find: /^@earendil-works\/pi-codemode\/declarations$/, replacement: src("codemode/src/declarations.ts") },
			{ find: /^@earendil-works\/pi-codemode\/source$/, replacement: src("codemode/src/source.ts") },
			{ find: /^@earendil-works\/pi-tui$/, replacement: src("tui/src/index.ts") },
			// The coding-agent's own sources and test utilities are not published under a
			// package name, so the harness names them through this prefix. It resolves
			// through the same handed root (PI_FORK) as the package aliases above: a literal
			// `../../.forks/pi/...` import in a spec resolves against the spec's own
			// location, which is the shared working copy however PI_FORK is set -- the
			// ambient input D38 forbids a verdict to depend on.
			{ find: /^@fork\/coding-agent\/(.+)$/, replacement: `${fork}/packages/coding-agent/$1` },
		],
	},
	test: {
		globals: true,
		environment: "node",
		testTimeout: 30000,
		env: { PI_OFFLINE: "1" },
		reporters: ["dot"],
		silent: "passed-only",
		include: ["tools/session-liveness/**/*.test.ts"],
		server: { deps: { external: [/@silvia-odwyer\/photon-node/] } },
	},
};
