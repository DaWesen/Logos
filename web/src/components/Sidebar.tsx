import { useState } from 'react'
import { NavLink, useNavigate } from 'react-router-dom'
import { MessageCircle, Bot, BookOpen, UserPlus, Settings, LogOut, ChevronLeft, ChevronRight, FileText, Activity, Wallet, Network, Plug } from 'lucide-react'
import { useAuth } from '@/hooks/useAuth'
import { toMediaUrl } from '@/api/chat'
import './Sidebar.css'

const navItems = [
  { to: '/chat', icon: MessageCircle, label: '消息' },
  { to: '/bots', icon: Bot, label: 'Bot' },
  { to: '/knowledge', icon: BookOpen, label: '知识库' },
  { to: '/mcp', icon: Plug, label: 'MCP' },
  { to: '/graph', icon: Network, label: '知识图谱' },
  { to: '/friends', icon: UserPlus, label: '好友' },
  { to: '/monitoring', icon: Activity, label: '系统观测' },
  { to: '/billing', icon: Wallet, label: '账单' },
  { to: '/settings', icon: Settings, label: '设置' },
]

export default function Sidebar() {
  const [collapsed, setCollapsed] = useState(false)
  const { user, logout } = useAuth()
  const navigate = useNavigate()

  const handleLogout = () => {
    logout()
    navigate('/login')
  }

  return (
    <aside className={`sidebar ${collapsed ? 'sidebar-collapsed' : ''}`}>
      <div className="sidebar-header">
        <div className="sidebar-logo">
          <div className="logo-icon">A</div>
          {!collapsed && <span className="logo-text">AIM</span>}
        </div>
        <button className="sidebar-toggle" onClick={() => setCollapsed(!collapsed)}>
          {collapsed ? <ChevronRight size={16} /> : <ChevronLeft size={16} />}
        </button>
      </div>

      <nav className="sidebar-nav">
        {navItems.map(({ to, icon: Icon, label }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) => `sidebar-nav-item ${isActive ? 'active' : ''}`}
            title={label}
          >
            <Icon size={20} />
            {!collapsed && <span>{label}</span>}
          </NavLink>
        ))}
      </nav>

      <div className="sidebar-footer">
        <div className="sidebar-user" title={user?.nickname}>
          <div className="ba-avatar ba-avatar-sm">
            {user?.avatar ? (
              <img 
                src={toMediaUrl(user.avatar)} 
                alt="" 
                style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} 
              />
            ) : (
              user?.nickname?.charAt(0) || '?'
            )}
          </div>
          {!collapsed && (
            <div className="sidebar-user-info">
              <div className="sidebar-user-name">{user?.nickname || '未登录'}</div>
              <div className="sidebar-user-status">在线</div>
            </div>
          )}
        </div>
        <button className="sidebar-logout" onClick={handleLogout} title="退出">
          <LogOut size={18} />
        </button>
      </div>
    </aside>
  )
}
