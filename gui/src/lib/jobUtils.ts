/**
 * Shared utilities for job status display, formatting, and icons.
 * Used by JobsPage, JobDetailPage, and DashboardPage.
 */

import {
    Ban,
    CheckCircle,
    Clock,
    Loader,
    XCircle,
} from 'lucide-vue-next'

// ── Status helpers ────────────────────────────────────────────────────────────

export function statusVariant(status: string): 'default' | 'secondary' | 'destructive' | 'outline' {
    switch (status) {
        case 'succeeded': return 'outline'
        case 'running': return 'outline'
        case 'failed': return 'destructive'
        case 'pending': return 'outline'
        case 'cancelled': return 'outline'
        default: return 'secondary'
    }
}

export function statusClass(status: string): string {
    switch (status) {
        case 'succeeded': return 'bg-green-500/10 text-green-700 dark:text-green-400 border-green-500/20'
        case 'running': return 'bg-blue-500/10 text-blue-700 dark:text-blue-400 border-blue-500/20'
        case 'pending': return 'bg-amber-500/10 text-amber-700 dark:text-amber-400 border-amber-500/20'
        case 'cancelled': return 'bg-slate-500/10 text-slate-600 dark:text-slate-400 border-slate-500/20'
        default: return ''
    }
}

export function statusLabel(status: string): string {
    return status.charAt(0).toUpperCase() + status.slice(1)
}

/** Returns the Lucide icon component for a given job status. */
export function statusIcon(status: string) {
    switch (status) {
        case 'succeeded': return CheckCircle
        case 'running': return Loader
        case 'failed': return XCircle
        case 'cancelled': return Ban
        case 'pending':
        default: return Clock
    }
}

// ── Date / duration formatters ────────────────────────────────────────────────

/** Locale-formatted date+time string, or "—" if null. */
export function formatDate(iso: string | null | undefined): string {
    if (!iso) return '—'
    return new Date(iso).toLocaleString(undefined, {
        dateStyle: 'medium',
        timeStyle: 'short',
    })
}

/**
 * Human-readable elapsed time between two ISO timestamps.
 * If finishedAt is absent (job still running), calculates from startedAt to now.
 */
export function formatDuration(startedAt: string | null | undefined, finishedAt: string | null | undefined): string {
    if (!startedAt) return '—'
    const end = finishedAt ? new Date(finishedAt).getTime() : Date.now()
    const ms = end - new Date(startedAt).getTime()
    if (ms < 0) return '—'
    const s = Math.floor(ms / 1000)
    if (s < 60) return `${s}s`
    const m = Math.floor(s / 60)
    if (m < 60) return `${m}m ${s % 60}s`
    const h = Math.floor(m / 60)
    return `${h}h ${m % 60}m`
}

/** Human-readable byte size string. */
export function formatBytes(bytes: number): string {
    if (bytes === 0) return '0 B'
    const units = ['B', 'KB', 'MB', 'GB', 'TB']
    const i = Math.floor(Math.log(bytes) / Math.log(1024))
    return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`
}
