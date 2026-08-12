import { cva, type VariantProps } from "class-variance-authority";

import { cn } from "@/lib/utils";

const avatarVariants = cva(
  "inline-flex shrink-0 items-center justify-center rounded-full border border-border bg-surface-2 font-medium uppercase text-text-2",
  {
    variants: {
      size: {
        sm: "h-5 w-5 text-[10px]",
        md: "h-7 w-7 text-xs",
        lg: "h-9 w-9 text-sm",
      },
    },
    defaultVariants: { size: "md" },
  },
);

export interface AvatarProps extends VariantProps<typeof avatarVariants> {
  name?: string;
  src?: string | null;
  className?: string;
}

// Avatar: prefers src; falls back to the first letter of name (or "?") when missing.
// Doesn't use next/image — GitHub avatar URLs usually carry a ?s= size param, so a plain img works fine.
export function Avatar({ name, src, size, className }: AvatarProps) {
  const initial = (name?.trim()?.[0] ?? "?").toUpperCase();
  if (src) {
    return (
      // eslint-disable-next-line @next/next/no-img-element
      <img
        src={src}
        alt={name ?? "avatar"}
        className={cn(avatarVariants({ size }), "object-cover", className)}
      />
    );
  }
  return (
    <span
      aria-label={name ?? "user"}
      className={cn(avatarVariants({ size }), className)}
    >
      {initial}
    </span>
  );
}
