import { api } from '../api/client'
import type { MigrationJobView } from '../types'

// waitForJob polls GET /api/jobs/{id} until the job is final (SUCCESS/FAILED) or UNCERTAIN,
// which also needs a human. It resolves with the last job seen, or rejects after timeoutMs.
export async function waitForJob(jobId: string, intervalMs = 1000, timeoutMs = 120_000): Promise<MigrationJobView> {
  const deadline = Date.now() + timeoutMs
  for (;;) {
    const { job } = await api.getJob(jobId)
    if (job.done || job.status === 'UNCERTAIN') return job
    if (Date.now() > deadline) throw new Error('Job belum selesai. Cek statusnya lagi nanti di halaman riwayat.')
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}

// jobFeedback turns a finished job into the message shown to the user.
export function jobFeedback(job: MigrationJobView, label: string): { success: boolean; message: string } {
  switch (job.status) {
    case 'SUCCESS':
      return { success: true, message: `${label} berhasil dijalankan.` }
    case 'PARTIAL':
      return { success: false, message: `${label} berhenti sebagian (PARTIAL): ${job.last_error || 'lihat monitor batch'}. Lanjutkan atau batalkan dari halaman detail.` }
    case 'UNCERTAIN':
      return { success: false, message: `${label}: hasil di database target belum pasti. Migration berikutnya ditahan sampai Platform Admin memeriksanya.` }
    default:
      return { success: false, message: `${label} gagal: ${job.last_error || 'lihat riwayat migration'}` }
  }
}
