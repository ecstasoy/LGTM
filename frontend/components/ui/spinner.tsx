"use client";

import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";
import { useT } from "@/lib/i18n/context";

// Spinner: a spinning ring; uses currentColor so className controls it (e.g. text-muted).
const spinnerVariants = cva(
  "inline-block animate-spin rounded-full border-2 border-current border-t-transparent",
  {
    variants: {
      size: {
        xs: "h-3 w-3",
        sm: "h-4 w-4",
        md: "h-5 w-5",
        lg: "h-6 w-6",
      },
    },
    defaultVariants: { size: "sm" },
  },
);

export interface SpinnerProps extends VariantProps<typeof spinnerVariants> {
  className?: string;
  label?: string; // a11y: text exposed to screen readers
}

export function Spinner({ size, className, label }: SpinnerProps) {
  const t = useT();
  return (
    <span
      role="status"
      aria-label={label ?? t.ui.loadingLabel}
      className={cn(spinnerVariants({ size }), className)}
    />
  );
}
