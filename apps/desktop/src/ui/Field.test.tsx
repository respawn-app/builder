import { fireEvent, render, screen } from "@testing-library/react";
import { vi } from "vitest";

import { SelectField } from "./SelectField";

describe("Field", () => {
  it("renders SelectField through an island dropdown portal without native select markup", async () => {
    const onValueChange = vi.fn();

    render(
      <SelectField
        label="Source"
        onValueChange={onValueChange}
        options={[
          { label: "Main", value: "workspace-1" },
          { label: "Docs", value: "workspace-2" },
        ]}
        value="workspace-1"
      />,
    );

    const trigger = screen.getByRole("button", { name: "Source" });
    expect(trigger).toHaveAttribute("data-slot", "select-trigger");
    expect(trigger).toHaveAttribute("type", "button");

    fireEvent.pointerDown(trigger);
    const menu = await screen.findByRole("menu");
    expect(menu).toHaveClass(
      "island-surface",
      "island-surface-3",
      "w-[var(--radix-dropdown-menu-trigger-width)]",
      "overflow-y-auto",
    );
    expect(document.body).toContainElement(menu);

    fireEvent.click(screen.getByRole("menuitemradio", { name: "Docs" }));

    expect(onValueChange).toHaveBeenCalledWith("workspace-2");
  });
});
