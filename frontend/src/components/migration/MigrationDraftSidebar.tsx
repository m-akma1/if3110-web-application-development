import React from 'react'
import { MigrationView } from '../../types'
import { fmtTime } from '../../utils/migrationFormat'

interface MigrationDraftSidebarProps {
  drafts: MigrationView[]
  selectedDraft: MigrationView | null
  onSelectDraft: (d: MigrationView | null) => void
}

export const MigrationDraftSidebar: React.FC<MigrationDraftSidebarProps> = ({
  drafts,
  selectedDraft,
  onSelectDraft,
}) => {
  return (
    <div className="bg-app-card border border-app-border rounded-xl p-3 space-y-2 h-fit">
      <div className="flex items-center justify-between px-1">
        <h3 className="text-xs font-bold text-zinc-400 uppercase tracking-wider">Draft</h3>
        <button
          type="button"
          onClick={() => onSelectDraft(null)}
          className="text-[11px] font-semibold text-brand-primary hover:underline cursor-pointer"
        >
          + Draft baru
        </button>
      </div>
      {drafts.length === 0 ? (
        <p className="px-1 py-2 text-[11px] text-zinc-500">Belum ada draft.</p>
      ) : (
        drafts.map((d) => {
          const isSelected = selectedDraft?.id === d.id
          return (
            <button
              key={d.id}
              type="button"
              onClick={() => onSelectDraft(d)}
              className={`w-full text-left block px-3 py-2 rounded-lg border text-xs transition cursor-pointer ${
                isSelected
                  ? 'bg-app-active border-app-border-strong text-zinc-100'
                  : 'border-transparent text-zinc-300 hover:bg-app-hover'
              }`}
            >
              <div className="flex items-center justify-between gap-1">
                <span className="font-semibold truncate">{d.name}</span>
                {d.status === 'PENDING_REVIEW' && (
                  <span className="text-[9px] px-1 py-0.2 rounded bg-sem-warning/20 text-sem-warning font-semibold shrink-0">
                    Review
                  </span>
                )}
                {d.status === 'APPROVED' && (
                  <span className="text-[9px] px-1 py-0.2 rounded bg-sem-success/20 text-sem-success font-semibold shrink-0">
                    Approved
                  </span>
                )}
                {d.status === 'REJECTED' && (
                  <span className="text-[9px] px-1 py-0.2 rounded bg-sem-danger/20 text-sem-danger font-semibold shrink-0">
                    Rejected
                  </span>
                )}
              </div>
              <div className="text-[10px] text-zinc-500 mt-0.5">
                {d.created_by_name || '-'} · {fmtTime(d.updated_at)}
              </div>
            </button>
          )
        })
      )}
    </div>
  )
}
