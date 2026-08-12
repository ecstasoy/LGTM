import { NavBar } from "@/components/NavBar";
import { Footer } from "@/components/Footer";

// (main) route group layout: used by landing (`/`) and history (`/history`).
// Global NavBar on top + centered main area capped at max-w-5xl + Footer at the bottom.
// /review/[id] isn't under (main), so it skips this layout and controls its own edge-to-edge dashboard layout.
export default function MainGroupLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    <div className="flex min-h-screen flex-col">
      <NavBar />
      <main className="mx-auto w-full max-w-5xl flex-1 px-6 py-8">{children}</main>
      <Footer />
    </div>
  );
}
