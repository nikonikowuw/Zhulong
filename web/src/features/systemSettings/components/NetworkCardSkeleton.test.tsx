import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { NetworkCardSkeleton } from "./NetworkCardSkeleton";

describe("NetworkCardSkeleton", () => {
  it("renders with aria-hidden and pulse animation styles", () => {
    const { container } = render(<NetworkCardSkeleton />);
    const skeletonEl = container.firstChild as HTMLElement;

    expect(skeletonEl).toBeInTheDocument();
    expect(skeletonEl).toHaveAttribute("aria-hidden", "true");
    expect(skeletonEl.className).toContain("animate-pulse");
  });
});
