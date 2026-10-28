import { useState, useEffect } from 'react'
import { Wallet, Cpu, HardDrive, Wifi, ArrowUpRight, ArrowDownLeft, RefreshCw } from 'lucide-react'
import { billingApi, Account, Transaction, PaymentOrder, PaymentOrderStatus } from '@/api/billing'
import './WalletPage.css'

export default function WalletPage() {
  const [account, setAccount] = useState<Account | null>(null)
  const [transactions, setTransactions] = useState<Transaction[]>([])
  const [usageStats, setUsageStats] = useState<Record<string, number>>({})
  const [loading, setLoading] = useState(true)
  const [depositAmount, setDepositAmount] = useState('')
  const [depositing, setDepositing] = useState(false)
  const [paymentOrder, setPaymentOrder] = useState<PaymentOrder | null>(null)
  const [refreshingOrder, setRefreshingOrder] = useState(false)

  const loadData = async () => {
    try {
      setLoading(true)
      const [accountData, transactionsData, statsData] = await Promise.all([
        billingApi.getAccount(),
        billingApi.getTransactions({ page_size: 20 }),
        billingApi.getUsageStats()
      ])
      setAccount(accountData)
      setTransactions(transactionsData.transactions || [])
      setUsageStats(statsData?.stats || {})
    } catch (error) {
      console.error('Failed to load wallet data:', error)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    loadData()
  }, [])

  const handleDeposit = async (e: React.FormEvent) => {
    e.preventDefault()
    const amount = parseFloat(depositAmount)
    if (!amount || amount <= 0) return

    try {
      setDepositing(true)
      // 只创建支付单：金额由支付渠道回调入账，前端无法直接给账户加钱
      const order = await billingApi.createPaymentOrder({
        amount,
        payment_method: 'alipay'
      })
      if (!order) {
        alert('创建支付单失败，请重试')
        return
      }
      setPaymentOrder(order)
      setDepositAmount('')
    } catch (error) {
      console.error('Failed to create payment order:', error)
      alert(error instanceof Error ? error.message : '创建支付单失败，请重试')
    } finally {
      setDepositing(false)
    }
  }

  const refreshPaymentOrder = async () => {
    if (!paymentOrder) return
    try {
      setRefreshingOrder(true)
      const latest = await billingApi.getPaymentOrder(paymentOrder.id)
      if (latest) {
        setPaymentOrder(latest)
        if (latest.status === PaymentOrderStatus.PAID) await loadData()
      }
    } catch (error) {
      console.error('Failed to refresh payment order:', error)
    } finally {
      setRefreshingOrder(false)
    }
  }

  // 待支付时轮询订单状态，渠道回调到账后自动刷新余额
  useEffect(() => {
    if (!paymentOrder || paymentOrder.status !== PaymentOrderStatus.PENDING) return
    const orderId = paymentOrder.id
    const timer = setInterval(async () => {
      try {
        const latest = await billingApi.getPaymentOrder(orderId)
        if (!latest) return
        setPaymentOrder(latest)
        if (latest.status === PaymentOrderStatus.PAID) await loadData()
      } catch (error) {
        console.error('Failed to poll payment order:', error)
      }
    }, 5000)
    return () => clearInterval(timer)
  }, [paymentOrder?.id, paymentOrder?.status])

  const getPaymentStatusLabel = (status: number) => {
    const labels: Record<number, string> = {
      [PaymentOrderStatus.PENDING]: '待支付',
      [PaymentOrderStatus.PAID]: '已支付',
      [PaymentOrderStatus.FAILED]: '支付失败',
      [PaymentOrderStatus.EXPIRED]: '已过期'
    }
    return labels[status] || '未知'
  }

  const formatAmount = (amount: number | undefined) => {
    if (amount === undefined || amount === null || isNaN(amount)) return '0.000000'
    return Math.abs(amount).toFixed(6)
  }

  const formatDate = (dateStr: string | undefined) => {
    if (!dateStr) return ''
    const d = new Date(dateStr)
    if (isNaN(d.getTime())) return dateStr
    return d.toLocaleString()
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
    return <div className="wallet-loading">加载中...</div>
  }

  return (
    <div className="wallet-page">
      <h1>钱包中心</h1>

      <div className="wallet-balance-card">
        <div className="wallet-balance-icon">
          <Wallet size={32} />
        </div>
        <div className="wallet-balance-info">
          <span className="wallet-balance-label">账户余额</span>
          <span className="wallet-balance-amount">¥{account?.balance?.toFixed(2) || '0.00'}</span>
        </div>
        <form onSubmit={handleDeposit} className="wallet-deposit-form">
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
            {depositing ? '生成中...' : '充值'}
          </button>
        </form>
      </div>

      {paymentOrder && (
        <div className="wallet-payment-order">
          <div className="wallet-payment-order-header">
            <span className="wallet-payment-order-id">支付单号 {paymentOrder.id}</span>
            <span className={`wallet-payment-order-status wallet-payment-order-status-${paymentOrder.status}`}>
              {getPaymentStatusLabel(paymentOrder.status)}
            </span>
          </div>
          <div className="wallet-payment-order-meta">
            金额 ¥{paymentOrder.amount.toFixed(2)} · 支付方式 {paymentOrder.payment_method}
            {paymentOrder.status === PaymentOrderStatus.PENDING && paymentOrder.expires_at && (
              <> · 有效期至 {new Date(paymentOrder.expires_at).toLocaleTimeString()}</>
            )}
          </div>
          <div className="wallet-payment-order-tip">
            请通过 {paymentOrder.payment_method} 完成支付，渠道回调成功后余额自动到账（本页会自动刷新状态）。
          </div>
          <div className="wallet-payment-order-actions">
            <button type="button" onClick={refreshPaymentOrder} disabled={refreshingOrder}>
              {refreshingOrder ? '刷新中...' : '刷新支付状态'}
            </button>
            <button type="button" className="wallet-payment-order-close" onClick={() => setPaymentOrder(null)}>
              关闭
            </button>
          </div>
        </div>
      )}

      <div className="wallet-stats-section">
        <h2>使用统计</h2>
        <div className="wallet-stats-grid">
          <div className="wallet-stat-card">
            <div className="wallet-stat-icon wallet-stat-model">
              <Cpu size={20} />
            </div>
            <div className="wallet-stat-info">
              <span className="wallet-stat-label">模型调用费用</span>
              <span className="wallet-stat-value">¥{(usageStats.model_call_cost || 0).toFixed(6)}</span>
            </div>
          </div>
          <div className="wallet-stat-card">
            <div className="wallet-stat-icon wallet-stat-storage">
              <HardDrive size={20} />
            </div>
            <div className="wallet-stat-info">
              <span className="wallet-stat-label">存储费用</span>
              <span className="wallet-stat-value">¥{(usageStats.storage_cost || 0).toFixed(6)}</span>
            </div>
          </div>
          <div className="wallet-stat-card">
            <div className="wallet-stat-icon wallet-stat-bandwidth">
              <Wifi size={20} />
            </div>
            <div className="wallet-stat-info">
              <span className="wallet-stat-label">带宽费用</span>
              <span className="wallet-stat-value">¥{(usageStats.bandwidth_cost || 0).toFixed(6)}</span>
            </div>
          </div>
        </div>
      </div>

      <div className="wallet-transactions-section">
        <div className="wallet-section-header">
          <h2>交易记录</h2>
          <button className="wallet-refresh-btn" onClick={loadData} title="刷新">
            <RefreshCw size={16} />
          </button>
        </div>
        <div className="wallet-transactions-list">
          {!transactions || transactions.length === 0 ? (
            <div className="wallet-no-transactions">暂无交易记录</div>
          ) : (
            transactions.map((tx) => (
              <div key={tx.id} className="wallet-transaction-item">
                <div className="wallet-transaction-left">
                  <div className={`wallet-transaction-icon wallet-transaction-type-${getTransactionTypeColor(tx.type)}`}>
                    {tx.type === 1 || tx.type === 4 ? <ArrowDownLeft size={16} /> : <ArrowUpRight size={16} />}
                  </div>
                  <div className="wallet-transaction-detail">
                    <span className="wallet-transaction-desc">{tx.description || getTransactionTypeLabel(tx.type)}</span>
                    <span className="wallet-transaction-type-label">{getTransactionTypeLabel(tx.type)}</span>
                  </div>
                </div>
                <div className="wallet-transaction-right">
                  <span className={`wallet-transaction-amount ${tx.type === 1 || tx.type === 4 ? 'positive' : 'negative'}`}>
                    {tx.amount === 0 ? '' : (tx.type === 1 || tx.type === 4 ? '+' : '-')}{formatAmount(tx.amount)}
                  </span>
                  <span className="wallet-transaction-time">
                    {formatDate(tx.created_at)}
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
