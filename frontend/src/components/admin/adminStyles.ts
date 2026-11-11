export const adminInput =
  'w-full px-3 py-2 bg-app-input border border-app-border rounded-xl text-xs text-white placeholder:text-zinc-500 focus:ring-2 focus:ring-brand-primary/50 focus:border-brand-primary focus:outline-none'
export const adminSelect =
  'bg-app-input border border-app-border text-zinc-200 text-xs rounded-lg px-2 py-1.5 focus:outline-none focus:border-brand-primary cursor-pointer'
export const adminLabel = 'block text-xs font-medium text-zinc-300 mb-1'
export const btnPrimary =
  'px-3 py-2 rounded-xl bg-brand-primary hover:bg-brand-primary-hover text-app-bg text-xs font-bold transition cursor-pointer'
export const btnSmall =
  'px-2.5 py-1 rounded-lg bg-app-active hover:bg-app-border-strong text-zinc-200 text-[11px] font-semibold transition cursor-pointer'
export const btnDanger =
  'px-2.5 py-1 rounded-lg bg-sem-danger/10 hover:bg-sem-danger/20 text-sem-danger text-[11px] font-semibold border border-sem-danger/30 transition cursor-pointer'

export function levelLabel(level: string): string {
  switch (level) {
    case 'VIEW_EDIT':
      return 'View + Edit'
    case 'VIEW_ONLY':
      return 'View Only'
    default:
      return 'Tanpa akses'
  }
}
