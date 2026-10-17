import * as React from "react"
import { cva, type VariantProps } from "class-variance-authority"
import { cn } from "@/lib/utils"
import { Slot } from "radix-ui"

const buttonVariants = cva("ui-button", {
  variants: {
    variant: {
      default: "primary", outline: "", secondary: "secondary", ghost: "text-button",
      destructive: "danger-button", link: "text-button",
    },
    size: {
      default: "", xs: "button-sm", sm: "button-sm", lg: "button-lg",
      icon: "icon-button", "icon-xs": "icon-button button-sm", "icon-sm": "icon-button button-sm", "icon-lg": "icon-button button-lg",
    },
  },
  defaultVariants: { variant: "default", size: "default" },
})

function Button({
  className,
  variant = "default",
  size = "default",
  asChild = false,
  ...props
}: React.ComponentProps<"button"> &
  VariantProps<typeof buttonVariants> & {
    asChild?: boolean
  }) {
  const Comp = asChild ? Slot.Root : "button"

  return (
    <Comp
      data-slot="button"
      data-variant={variant}
      data-size={size}
      className={cn(buttonVariants({ variant, size, className }))}
      {...props}
    />
  )
}

export { Button, buttonVariants }
