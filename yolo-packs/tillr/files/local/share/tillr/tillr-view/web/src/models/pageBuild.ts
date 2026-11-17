// Vite replaces this at build time, so the page's identity does not come from the server.
declare const __TILLR_PAGE_BUILD__: string;
export const PAGE_BUILD = typeof __TILLR_PAGE_BUILD__ === "undefined" ? "development" : __TILLR_PAGE_BUILD__;
