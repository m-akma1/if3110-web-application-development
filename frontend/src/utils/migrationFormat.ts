export function fmtTime(t?: string): string {
  if (!t || t.startsWith('0001-01-01')) {
    return '-'
  }
  const date = new Date(t)
  if (isNaN(date.getTime())) return t
  const yyyy = date.getFullYear()
  const mm = String(date.getMonth() + 1).padStart(2, '0')
  const dd = String(date.getDate()).padStart(2, '0')
  const hh = String(date.getHours()).padStart(2, '0')
  const min = String(date.getMinutes()).padStart(2, '0')
  const ss = String(date.getSeconds()).padStart(2, '0')
  return `${yyyy}-${mm}-${dd} ${hh}:${min}:${ss}`
}

export function versionLabel(v: number): string {
  return v > 0 ? `v${v}` : '-'
}
