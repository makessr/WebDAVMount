export type DrivePlan = {
  rows: {id: string, name: string, drive: string}[]
  available: string[]
}

export function previewDriveChanges(plan: DrivePlan, targets: Record<string, string>) {
  const changes = plan.rows.map(row => ({id: row.id, from: row.drive, to: targets[row.id] ?? row.drive}))
  const changed = changes.filter(change => change.from !== change.to)
  const counts = new Map<string, number>()
  for (const change of changes) counts.set(change.to, (counts.get(change.to) ?? 0) + 1)
  const conflicts = changed.filter(change => !plan.available.includes(change.to) || (counts.get(change.to) ?? 0) > 1)
  const conflictIds = new Set(conflicts.map(change => change.id))
  return {changes, changed: changed.length, conflictIds}
}

export function canSwapDrives(plan: DrivePlan) {
  return plan.rows.length === 2 && plan.rows[0].drive !== plan.rows[1].drive &&
    plan.rows.every(row => plan.available.includes(row.drive))
}
