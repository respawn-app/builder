import { afterEach } from "vitest";

import { applyConfiguredTheme } from "./appEnvironment";

describe("applyConfiguredTheme", () => {
  afterEach(() => {
    document.documentElement.removeAttribute("data-builder-theme");
  });

  it("forces configured light and dark themes on the root element", () => {
    applyConfiguredTheme("light");
    expect(document.documentElement).toHaveAttribute("data-builder-theme", "light");

    applyConfiguredTheme("dark");
    expect(document.documentElement).toHaveAttribute("data-builder-theme", "dark");
  });

  it("keeps system theme when config is auto or invalid", () => {
    applyConfiguredTheme("dark");

    applyConfiguredTheme("auto");
    expect(document.documentElement).not.toHaveAttribute("data-builder-theme");

    applyConfiguredTheme("unknown");
    expect(document.documentElement).not.toHaveAttribute("data-builder-theme");
  });
});
