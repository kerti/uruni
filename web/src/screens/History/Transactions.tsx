import { useEffect } from 'react'

import ErrorState from '@/components/states/ErrorState'
import Loading from '@/components/states/Loading'
import TransactionList from '@/components/TransactionList'
import { copy } from '@/copy/id'
import { getBalances } from '@/lib/balances'
import { listTransactions } from '@/lib/transactions'
import { useApi } from '@/lib/useApi'
import type { Balances } from '@/lib/balances'
import type { Transaction } from '@/lib/transactions'

interface TransactionsData {
  balances: Balances
  transactions: Transaction[]
}

/**
 * Riwayat's Transaksi tab (M6.23, ADR-032): the full transaction list,
 * unchanged and unpaged in this slice - no limit, no search, no filters,
 * those are #225. Fetches the same two calls Home's recent-five peek makes
 * and reuses its row rendering through TransactionList rather than a second
 * copy of that markup.
 *
 * `refetchKey` is App.tsx's `location.key`, the same mechanism Home already
 * uses to pick up a transaction just recorded elsewhere.
 */
export default function Transactions({ refetchKey }: { refetchKey?: unknown }) {
  const [state, run] = useApi<TransactionsData>()

  async function loadTransactions(): Promise<TransactionsData> {
    const [balances, transactions] = await Promise.all([getBalances(), listTransactions()])
    return { balances, transactions }
  }

  useEffect(() => {
    void run(loadTransactions)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [run, refetchKey])

  if (state.status === 'idle' || state.status === 'loading') {
    return <Loading />
  }

  if (state.status === 'error' || !state.data) {
    return state.error ? <ErrorState error={state.error} onRetry={() => void run(loadTransactions)} /> : null
  }

  const { balances, transactions } = state.data
  const purposeNames = new Map(balances.purposes.map((purpose) => [purpose.id, purpose.name]))
  // GET /api/transactions answers oldest-first (Home.tsx's own note on the
  // same call) - reversed here for the same newest-first reading, just over
  // the whole list instead of a slice of it.
  const newestFirst = [...transactions].reverse()

  return <TransactionList transactions={newestFirst} purposeNames={purposeNames} emptyMessage={copy.home.recentActivityEmpty} />
}
