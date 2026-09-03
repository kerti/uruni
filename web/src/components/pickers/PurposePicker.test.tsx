import { render } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'

import PurposePicker from '@/components/pickers/PurposePicker'
import { selectOptionNames } from '@/test/select'
import type { Purpose } from '@/lib/purposes'

const purposes: Purpose[] = [
  { id: 1, kind: 'main', name: 'Kas utama', created_at: 1 },
  { id: 2, kind: 'pass_through', name: 'Kas Bidang', created_at: 1 },
]

describe('PurposePicker', () => {
  // Unlike AccountPicker there is nothing to exclude - a purpose is never
  // retired - so this is the whole list, read out of the open popup.
  it('lists every purpose the fund has', async () => {
    render(<PurposePicker id="purpose" label="Peruntukan" purposes={purposes} value={1} onChange={vi.fn()} />)

    expect(await selectOptionNames('Peruntukan')).toEqual(['Kas utama', 'Kas Bidang'])
  })
})
