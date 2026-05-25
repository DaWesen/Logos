import { useState, useEffect } from 'react'
import { billingApi, Account, Transaction } from '../api/billing'
import './BillingPage.css'

export function BillingPage() {
  const [account, setAccount] = useState<Account | null>(null)
  const [transactions, setTransactions] = useState<Transaction[]>([])
  const [loading, setLoading] = useState(true)
  const [depositAmount, setDepositAmount] = useState('')
  const [depositing, setDepositing] = useState(false)

  const loadData = async () => {
    try {
      setLoading(true)
      const [accountData, transactionsData] = await Promise.all([
        billingApi.getAccount(),
        billingApi.getTransactions({ page_size: 20 })
      ])
      setAccount(accountData)
      setTransactions(transactionsData.transactions || [])
    } catch (error) {
      console.error('Failed to load billing data:', error)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadData()
  }, [])

  const handleDeposit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (!depositAmount || parseFloat(depositAmount) <= 0) return

    try {
      setDepositing(true)
      await billingApi.deposit({
        amount: parseFloat(depositAmount),
        payment_method: 'manual'
      })
      setDepositAmount('')
      await loadData()
    } catch (error) {
      console.error('Failed to deposit:', error)
      alert('充值失败，请重试')
    } finally {
      setDepositing(false)
    }
  }

  const getTransactionTypeLabel = (type: number) => {
    const labels: Record<number, string> = {
      0: '未知',
      1: '充值',
      2: '提现',
      3: '消费',
      4: '退款'
    }
    return labels[type] || '未知'
  }

  const getTransactionTypeColor = (type: number) => {
    const colors: Record<number, string> = {
      1: 'green',
      2: 'red',
      3: 'orange',
      4: 'blue'
    }
    return colors[type] || 'gray'
  }

  if (loading) {
    return <div className="billing-loading">加载中...</div>
  }

  return (
    <div className="billing-page">
      <h1>钱包中心</h1>

      <div className="billing-card">
        <h2>账户余额</h2>
        <div className="balance-display">
          <span className="balance-amount">¥{account?.balance?.toFixed(2) || '0.00'}</span>
        </div>

        <form onSubmit={handleDeposit} className="deposit-form">
          <input
            type="number"
            step="0.01"
            min="0.01"
            value={depositAmount}
            onChange={(e) => setDepositAmount(e.target.value)}
            placeholder="输入充值金额"
            disabled={depositing}
          />
          <button type="submit" disabled={depositing || !depositAmount}>
            {depositing ? '充值中...' : '充值'}
          </button>
        </form>
      </div>

      <div className="billing-card">
        <h2>使用说明</h2>
        <div className="usage-info">
          <p>📌 <strong>计费方式：</strong>使用平台模型时会自动扣费</p>
          <p>💡 <strong>使用步骤：</strong></p>
          <ol>
            <li>在此页面充值你的账户</li>
            <li>创建一个 Bot，选择 "平台" 作为模型提供商</li>
            <li>使用该 Bot 进行对话，系统会自动扣除相应费用</li>
            <li>在下方查看你的交易记录和消费详情</li>
          </ol>
        </div>
      </div>

      <div className="billing-card">
        <h2>交易记录</h2>
        <div className="transactions-list">
          {!transactions || transactions.length === 0 ? (
            <div className="no-transactions">暂无交易记录</div>
          ) : (
            transactions.map((tx) => (
              <div key={tx.id} className="transaction-item">
                <div className="transaction-left">
                  <span
                    className={`transaction-type transaction-type-${getTransactionTypeColor(tx.type)}`}
                  >
                    {getTransactionTypeLabel(tx.type)}
                  </span>
                  <span className="transaction-description">{tx.description}</span>
                </div>
                <div className="transaction-right">
                  <span
                    className={`transaction-amount ${
                      tx.type === 1 || tx.type === 4 ? 'positive' : 'negative'
                    }`}
                  >
                    {tx.type === 1 || tx.type === 4 ? '+' : '-'}{Math.abs(tx.amount).toFixed(6)}
                  </span>
                  <span className="transaction-time">
                    {new Date(tx.created_at).toLocaleString()}
                  </span>
                </div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  )
}
