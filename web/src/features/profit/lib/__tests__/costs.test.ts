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
import { describe, expect, test } from 'vitest'

import type { ChannelCost } from '../../types'
import { getChannelCostUpdates, getCostKey } from '../costs'

const main: ChannelCost = {
  channel_id: 2,
  channel_name: 'main',
  channel_status: 1,
  model_name: 'gpt-image-2',
  unit_cost: 0.006,
}
const backup: ChannelCost = {
  channel_id: 6,
  channel_name: 'backup',
  channel_status: 1,
  model_name: 'gpt-image-2',
  unit_cost: null,
}
const costs = [main, backup]

describe('channel cost updates', () => {
  test('nothing is saved while no input was edited', () => {
    expect(getChannelCostUpdates(costs, {})).toEqual([])
  })

  test('an edited input saves its cost and an untouched one is left out', () => {
    expect(
      getChannelCostUpdates(costs, { [getCostKey(backup)]: ' 0.011 ' })
    ).toEqual([{ channel_id: 6, model_name: 'gpt-image-2', unit_cost: 0.011 }])
  })

  test('an input typed back to the saved cost is not saved again', () => {
    expect(
      getChannelCostUpdates(costs, {
        [getCostKey(main)]: '0.006',
        [getCostKey(backup)]: '',
      })
    ).toEqual([])
  })

  test('an emptied input removes the cost, and zero is a cost', () => {
    expect(
      getChannelCostUpdates(costs, {
        [getCostKey(main)]: '',
        [getCostKey(backup)]: '0',
      })
    ).toEqual([
      { channel_id: 2, model_name: 'gpt-image-2', unit_cost: null },
      { channel_id: 6, model_name: 'gpt-image-2', unit_cost: 0 },
    ])
  })

  test.each(['-0.01', 'abc', 'Infinity'])(
    'the input %s blocks saving',
    (value) => {
      expect(
        getChannelCostUpdates(costs, { [getCostKey(main)]: value })
      ).toBeNull()
    }
  )

  test('the same model at two channels keeps separate costs', () => {
    expect(getCostKey(main)).not.toBe(getCostKey(backup))
  })
})
