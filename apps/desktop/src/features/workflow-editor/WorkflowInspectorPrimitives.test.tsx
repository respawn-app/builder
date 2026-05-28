import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { initializeI18n } from "../../i18n/setup";
import { DetailSection, ValidationDetails } from "./WorkflowInspectorPrimitives";

void initializeI18n();

describe("ValidationDetails", () => {
  it("exposes titled inspector sections as named regions", () => {
    render(
      <DetailSection title="Route">
        <button type="button">Target node</button>
      </DetailSection>,
    );

    const routeSection = screen.getByRole("region", { name: "Route" });

    expect(within(routeSection).getByRole("heading", { level: 3, name: "Route" })).toBeInTheDocument();
    expect(within(routeSection).getByRole("button", { name: "Target node" })).toBeInTheDocument();
  });

  it("shows structured validation details in inspector cards", () => {
    render(
      <ValidationDetails
        errors={[
          {
            blocksContext: true,
            code: "workflow.validation.invalid_join_input_provider",
            details: {
              fieldName: "",
              inputName: "summary",
              placeholder: "",
              providerEdgeID: "edge-provider",
            },
            edgeID: "edge-provider",
            message: "Join input provider is invalid.",
            nodeID: "join",
            relatedIDs: [],
            transitionGroupID: "",
            workflowID: "workflow-1",
          },
        ]}
      />,
    );

    expect(screen.getAllByRole("listitem")).toHaveLength(1);
  });
});
