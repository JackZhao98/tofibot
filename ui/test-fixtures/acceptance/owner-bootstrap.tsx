import { createRoot } from "react-dom/client";
import { OwnerSessionGate } from "../../src/OwnerSession";

// The browser runner intercepts every request with synthetic responses.
createRoot(document.getElementById("root")!).render(
  <OwnerSessionGate><p>Synthetic authenticated workspace</p></OwnerSessionGate>,
);
