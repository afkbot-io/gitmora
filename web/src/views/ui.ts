/** Complete shared Tailwind variants; semantic markers support existing browser inspection. */
export const buttonStyles = {
  secondary:
    "secondary bg-paper text-ink border-control [&:hover:not(:disabled)]:bg-canvas [&:hover:not(:disabled)]:border-muted",
  primary:
    "primary bg-action text-white [&:hover:not(:disabled)]:bg-action-hover",
  text: "text-button px-0 py-1 min-h-8 bg-transparent border-0 hover:underline hover:text-accent-hover text-accent justify-start text-sm [&.mono]:whitespace-normal [&.mono]:text-left [&.mono]:wrap-anywhere [&.mono]:max-w-full",
  dangerOutline:
    "danger-outline bg-paper text-danger border-danger [&:hover:not(:disabled)]:bg-danger-soft",
  danger:
    "danger bg-danger-action text-white [&:hover:not(:disabled)]:bg-danger-action-hover",
} as const;

/** Shared layouts for forms, source details and integration settings. */
export const sharedStyles = {
  heading:
    "section-heading flex justify-between items-start gap-4 flex-wrap [&_h1]:mb-2 [&_h2]:mb-2 [&_>_div]:min-w-0 [&_>_div]:max-w-full",
  formGrid:
    "form-grid grid grid-cols-[repeat(2,_minmax(0,_1fr))] gap-4 mobile:grid-cols-[1fr]",
  setup:
    "setup-form [&_h3]:text-base [&_h3]:font-medium [&_h3]:mt-0 grid gap-4 mx-0 mt-4 mb-5 bg-paper border border-line p-5 rounded-lg w-full [&_h2]:mb-0 [&_label]:flex [&_label]:flex-col [&_label]:gap-1.5 [&_label]:mb-0 [&_label.checkbox]:flex-row [&_label.checkbox]:font-normal [&_label.checkbox]:items-center [&_label.checkbox]:mx-0 [&_label.checkbox]:my-0 [&_input]:w-full [&_select]:w-full [&_input[type=checkbox]]:w-[17px] [&_input[type=checkbox]]:shrink-0 [&_input[readonly]]:bg-canvas [&_input[readonly]]:text-muted mobile:p-4 [&_.scope-options_label.checkbox]:items-start [&_.scope-options_label.checkbox]:m-0 [&_.scope-options_label.checkbox]:gap-3",
  connectionScope:
    "connection-scope grid grid-cols-[155px_minmax(0,_1fr)] gap-x-4 gap-y-2 text-sm mx-0 mt-3 mb-6 [&_dt]:text-muted [&_dd]:wrap-anywhere mobile:grid-cols-[110px_minmax(0,_1fr)] mobile:text-xs mobile:gap-3",
  destructiveText:
    "text-button destructive hover:underline px-0 py-1 min-h-8 bg-transparent border-0 justify-start text-sm text-danger [&.mono]:whitespace-normal [&.mono]:text-left [&.mono]:wrap-anywhere [&.mono]:max-w-full",
  sourceDetails:
    "source-details grid grid-cols-[112px_minmax(0,_1fr)] gap-x-4 gap-y-2 text-xs px-0 py-4 border-t border-line mt-5 [&_dt]:text-muted mobile:grid-cols-[95px_minmax(0,_1fr)] mobile:gap-2",
  inlineForm:
    "inline-form [&_label]:flex [&_label]:flex-col [&_label]:gap-2 flex items-end gap-3 flex-wrap px-0 py-4 [&_label]:[flex:1] [&_label]:min-w-50 [&_label]:max-w-125 [&_label]:mb-0 [&_.notice]:w-full [&_.notice]:mb-0 [&_.error]:w-full [&_.error]:mb-0",
} as const;
