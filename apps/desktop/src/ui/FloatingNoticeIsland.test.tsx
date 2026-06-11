import { fireEvent, render, screen } from "@testing-library/react";
import { vi } from "vitest";

import { FloatingNoticeIsland } from "./FloatingNoticeIsland";

describe("FloatingNoticeIsland", () => {
  it("keeps expanded content mounted while collapsed", () => {
    const onCollapsedChange = vi.fn();

    render(
      <FloatingNoticeIsland
        collapsed
        collapseLabel="Collapse"
        expandLabel="Expand"
        onCollapsedChange={onCollapsedChange}
        title="Notice"
      >
        <p>Persistent notice body</p>
      </FloatingNoticeIsland>,
    );

    const content = screen.getByTestId("floating-notice-content");
    expect(content).toHaveAttribute("aria-hidden", "true");
    expect(content).toHaveAttribute("inert");
    expect(screen.getByText("Persistent notice body")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Expand" }));

    expect(onCollapsedChange).toHaveBeenCalledWith(false);
  });

  it("keeps the collapsed affordance mounted while expanded", () => {
    render(
      <FloatingNoticeIsland
        collapsed={false}
        collapseLabel="Collapse"
        expandLabel="Expand"
        onCollapsedChange={vi.fn()}
        title="Notice"
      >
        <p>Visible notice body</p>
      </FloatingNoticeIsland>,
    );

    const collapsedButton = screen.getByTestId("floating-notice-collapsed-button");
    expect(collapsedButton).toHaveAttribute("inert");
    expect(screen.getByTestId("floating-notice-content")).not.toHaveAttribute("aria-hidden", "true");
  });
});
