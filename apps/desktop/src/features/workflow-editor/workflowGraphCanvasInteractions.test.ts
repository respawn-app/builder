import { afterEach, describe, expect, it, vi } from "vitest";

import { groupIDFromPoint } from "./workflowGraphCanvasInteractions";

describe("workflowGraphCanvasInteractions", () => {
  afterEach(() => {
    document.body.replaceChildren();
    vi.restoreAllMocks();
  });

  it("falls back to group bounds when a dragged card covers the pointer target", () => {
    const draggedCard = document.createElement("div");
    document.body.append(groupElement("group-a", rect(10, 20, 200, 160)), draggedCard);
    installElementFromPoint(draggedCard);

    expect(groupIDFromPoint(80, 90)).toBe("group-a");
  });

  it("chooses the smallest containing group when fallback bounds overlap", () => {
    const draggedCard = document.createElement("div");
    document.body.append(
      groupElement("outer-group", rect(0, 0, 300, 300)),
      groupElement("inner-group", rect(50, 50, 100, 100)),
      draggedCard,
    );
    installElementFromPoint(draggedCard);

    expect(groupIDFromPoint(75, 75)).toBe("inner-group");
    expect(groupIDFromPoint(25, 25)).toBe("outer-group");
  });
});

function groupElement(groupID: string, bounds: DOMRect): HTMLElement {
  const element = document.createElement("section");
  element.dataset.workflowGroupId = groupID;
  Object.defineProperty(element, "getBoundingClientRect", {
    configurable: true,
    value: () => bounds,
  });
  return element;
}

function installElementFromPoint(element: Element): void {
  Object.defineProperty(document, "elementFromPoint", {
    configurable: true,
    value: vi.fn<typeof document.elementFromPoint>(() => element),
  });
}

function rect(x: number, y: number, width: number, height: number): DOMRect {
  const view = document.defaultView;
  if (view === null) {
    throw new Error("Expected test document to have a default window");
  }
  return new view.DOMRect(x, y, width, height);
}
