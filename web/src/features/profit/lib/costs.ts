/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import type { ChannelCost, ChannelCostUpdate } from '../types'

/** Identifies the cost of one model at one channel. */
export function getCostKey(
  cost: Pick<ChannelCost, 'channel_id' | 'model_name'>
): string {
  return `${cost.channel_id}\u0000${cost.model_name}`
}

/**
 * The costs to save for the edited inputs: an emptied input removes the cost,
 * an unchanged one is left out. Returns null when an input is not a number of
 * 0 or more, so nothing is saved until it is corrected.
 */
export function getChannelCostUpdates(
  costs: ChannelCost[],
  drafts: Record<string, string>
): ChannelCostUpdate[] | null {
  const updates: ChannelCostUpdate[] = []
  for (const cost of costs) {
    const draft = drafts[getCostKey(cost)]
    if (draft === undefined) continue

    const text = draft.trim()
    const unitCost = text === '' ? null : Number(text)
    if (unitCost !== null && (!Number.isFinite(unitCost) || unitCost < 0)) {
      return null
    }
    if (unitCost === cost.unit_cost) continue
    updates.push({
      channel_id: cost.channel_id,
      model_name: cost.model_name,
      unit_cost: unitCost,
    })
  }
  return updates
}
