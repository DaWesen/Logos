import { useState, useEffect, useCallback } from 'react'
import {
  UserPlus, Search, Trash2, ChevronRight, ChevronDown,
  Ban, ShieldOff, Edit3, Check, X, FolderPlus, Users, Bell
} from 'lucide-react'
import { searchUsers, getUser } from '@/api/user'
import {
  getFriendList, addFriend, deleteFriend, getFriendRequests,
  handleFriendRequest, getOnlineStatus, updateFriendRemark,
  getFriendGroups, createFriendGroup, deleteFriendGroup,
  updateFriendGroup, moveFriendToGroup, blockUser, unblockUser,
  getBlacklist
} from '@/api/friend'
import './FriendsPage.css'

type TabKey = 'friends' | 'requests' | 'blacklist'

interface FriendItem {
  friend_id: string
  remark: string
  group_id: string
  nickname?: string
  avatar?: string
  username?: string
}

interface FriendGroupItem {
  id: string
  name: string
  sort_order: number
}

interface FriendRequestItem {
  id: string
  from_user_id: string
  to_user_id: string
  remark: string
  message: string
  status: string
  created_at?: string
}

interface BlacklistItem {
  id: string
  blocked_user_id: string
  created_at?: string
}

export default function FriendsPage() {
  const [activeTab, setActiveTab] = useState<TabKey>('friends')
  const [friends, setFriends] = useState<FriendItem[]>([])
  const [groups, setGroups] = useState<FriendGroupItem[]>([])
  const [requests, setRequests] = useState<FriendRequestItem[]>([])
  const [blacklist, setBlacklist] = useState<BlacklistItem[]>([])
  const [onlineStatuses, setOnlineStatuses] = useState<Record<string, string>>({})
  const [expandedGroups, setExpandedGroups] = useState<Record<string, boolean>>({ default: true })
  const [selectedFriend, setSelectedFriend] = useState<FriendItem | null>(null)
  const [editingRemark, setEditingRemark] = useState(false)
  const [remarkValue, setRemarkValue] = useState('')
  const [showSearch, setShowSearch] = useState(false)
  const [searchKeyword, setSearchKeyword] = useState('')
  const [searchResults, setSearchResults] = useState<{ id: string; nickname: string; username: string }[]>([])
  const [showAddMessage, setShowAddMessage] = useState<string | null>(null)
  const [addMessage, setAddMessage] = useState('')
  const [showNewGroup, setShowNewGroup] = useState(false)
  const [newGroupName, setNewGroupName] = useState('')
  const [editingGroup, setEditingGroup] = useState<string | null>(null)
  const [editingGroupName, setEditingGroupName] = useState('')
  const [movingFriend, setMovingFriend] = useState<string | null>(null)
  const [friendUserInfo, setFriendUserInfo] = useState<Record<string, { nickname: string; avatar: string }>>({})

  const loadFriends = useCallback(async () => {
    const [friendList, groupList] = await Promise.all([getFriendList(), getFriendGroups()])
    const friends = friendList as FriendItem[]
    setFriends(friends)
    setGroups(groupList as FriendGroupItem[])
    const infoMap: Record<string, { nickname: string; avatar: string }> = {}
    await Promise.all(friends.map(async (f) => {
      try {
        const userInfo = await getUser(f.friend_id)
        if (userInfo) {
          infoMap[f.friend_id] = {
            nickname: (typeof userInfo.nickname === 'string' ? userInfo.nickname : '') || (typeof userInfo.username === 'string' ? userInfo.username : ''),
            avatar: typeof userInfo.avatar === 'string' ? userInfo.avatar : '',
          }
        }
      } catch {}
    }))
    setFriendUserInfo(infoMap)
  }, [])

  const loadRequests = useCallback(async () => {
    const list = await getFriendRequests()
    setRequests(list as FriendRequestItem[])
  }, [])

  const loadBlacklist = useCallback(async () => {
    const list = await getBlacklist()
    setBlacklist(list as BlacklistItem[])
  }, [])

  const refreshOnlineStatuses = useCallback(async (friendList: FriendItem[]) => {
    if (friendList.length === 0) return
    const ids = friendList.map((f) => f.friend_id)
    const statuses = await getOnlineStatus(ids)
    if (statuses && typeof statuses === 'object') {
      setOnlineStatuses(statuses)
    }
  }, [])

  useEffect(() => {
    loadFriends()
    loadRequests()
    loadBlacklist()
  }, [loadFriends, loadRequests, loadBlacklist])

  useEffect(() => {
    refreshOnlineStatuses(friends)
    const interval = setInterval(() => refreshOnlineStatuses(friends), 30000)
    return () => clearInterval(interval)
  }, [friends, refreshOnlineStatuses])

  const pendingRequests = requests.filter((r) => r.status === 'PENDING' || r.status === 'FRIEND_REQUEST_STATUS_PENDING' || r.status === '1')

  const friendsByGroup = () => {
    const grouped: Record<string, FriendItem[]> = { default: [] }
    groups.forEach((g) => {
      grouped[g.id] = []
    })
    friends.forEach((f) => {
      const gid = f.group_id || 'default'
      if (!grouped[gid]) grouped[gid] = []
      grouped[gid].push(f)
    })
    return grouped
  }

  const getGroupName = (gid: string) => {
    if (gid === 'default') return '默认分组'
    const g = groups.find((g) => g.id === gid)
    return g ? g.name : '未分组'
  }

  const toggleGroup = (gid: string) => {
    setExpandedGroups((prev) => ({ ...prev, [gid]: !prev[gid] }))
  }

  const handleSearch = async () => {
    if (!searchKeyword.trim()) return
    const results = await searchUsers(searchKeyword.trim())
    setSearchResults(Array.isArray(results) ? results : [])
  }

  const handleAddFriend = async (userId: string) => {
    try {
      await addFriend(userId, '', addMessage)
      setShowAddMessage(null)
      setAddMessage('')
      setShowSearch(false)
      setSearchKeyword('')
      setSearchResults([])
      loadRequests()
    } catch {
      // ignore
    }
  }

  const handleDeleteFriend = async (friendId: string) => {
    try {
      await deleteFriend(friendId)
      setSelectedFriend(null)
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleUpdateRemark = async () => {
    if (!selectedFriend) return
    try {
      await updateFriendRemark(selectedFriend.friend_id, remarkValue)
      setEditingRemark(false)
      setSelectedFriend({ ...selectedFriend, remark: remarkValue })
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleCreateGroup = async () => {
    if (!newGroupName.trim()) return
    try {
      await createFriendGroup(newGroupName.trim())
      setShowNewGroup(false)
      setNewGroupName('')
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleDeleteGroup = async (groupId: string) => {
    try {
      await deleteFriendGroup(groupId)
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleUpdateGroup = async (groupId: string) => {
    if (!editingGroupName.trim()) return
    try {
      await updateFriendGroup(groupId, editingGroupName.trim(), 0)
      setEditingGroup(null)
      setEditingGroupName('')
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleMoveFriend = async (friendId: string, groupId: string) => {
    try {
      await moveFriendToGroup(friendId, groupId)
      setMovingFriend(null)
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleBlockUser = async (userId: string) => {
    try {
      await blockUser(userId)
      loadBlacklist()
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleUnblockUser = async (userId: string) => {
    try {
      await unblockUser(userId)
      loadBlacklist()
    } catch {
      // ignore
    }
  }

  const handleAcceptRequest = async (requestId: string) => {
    try {
      await handleFriendRequest(requestId, 'accepted')
      loadRequests()
      loadFriends()
    } catch {
      // ignore
    }
  }

  const handleRejectRequest = async (requestId: string) => {
    try {
      await handleFriendRequest(requestId, 'rejected')
      loadRequests()
    } catch {
      // ignore
    }
  }

  const statusColor = (status: string) => {
    if (status === 'online') return 'var(--ba-success)'
    if (status === 'busy') return 'var(--ba-accent)'
    if (status === 'away') return 'var(--ba-warning)'
    return 'var(--ba-text-light)'
  }

  const statusLabel = (status: string) => {
    if (status === 'online') return '在线'
    if (status === 'busy') return '忙碌'
    if (status === 'away') return '离开'
    return '离线'
  }

  const grouped = friendsByGroup()

  return (
    <div className="friends-page">
      <div className="friends-header">
        <div>
          <h2>联系人</h2>
          <p className="friends-subtitle">管理好友、分组与黑名单</p>
        </div>
        <div className="friends-header-actions">
          <button className="ba-btn ba-btn-primary" onClick={() => setShowSearch(true)}>
            <UserPlus size={16} /> 添加好友
          </button>
          <button className="ba-btn" onClick={() => setShowNewGroup(true)}>
            <FolderPlus size={16} /> 新建分组
          </button>
        </div>
      </div>

      <div className="friends-tabs">
        <button
          className={`friends-tab ${activeTab === 'friends' ? 'active' : ''}`}
          onClick={() => setActiveTab('friends')}
        >
          <Users size={14} /> 好友列表
        </button>
        <button
          className={`friends-tab ${activeTab === 'requests' ? 'active' : ''}`}
          onClick={() => setActiveTab('requests')}
        >
          <Bell size={14} /> 好友申请
          {pendingRequests.length > 0 && (
            <span className="friends-tab-badge">{pendingRequests.length}</span>
          )}
        </button>
        <button
          className={`friends-tab ${activeTab === 'blacklist' ? 'active' : ''}`}
          onClick={() => setActiveTab('blacklist')}
        >
          <Ban size={14} /> 黑名单
        </button>
      </div>

      <div className="friends-body">
        {activeTab === 'friends' && (
          <div className="friends-layout">
            <div className="friends-sidebar">
              {Object.entries(grouped).map(([gid, groupFriends]) => {
                if (groupFriends.length === 0 && gid !== 'default') return null
                const isExpanded = expandedGroups[gid] !== false
                return (
                  <div key={gid} className="friend-group">
                    <div className="friend-group-header" onClick={() => toggleGroup(gid)}>
                      {isExpanded ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                      <span className="friend-group-name">{getGroupName(gid)}</span>
                      <span className="friend-group-count">{groupFriends.length}</span>
                      {editingGroup !== gid && (
                        <div className="friend-group-actions" onClick={(e) => e.stopPropagation()}>
                          <button
                            className="friend-group-action-btn"
                            onClick={() => {
                              setEditingGroup(gid)
                              setEditingGroupName(getGroupName(gid))
                            }}
                            title="重命名"
                          >
                            <Edit3 size={12} />
                          </button>
                          <button
                            className="friend-group-action-btn"
                            onClick={() => handleDeleteGroup(gid)}
                            title="删除分组"
                          >
                            <Trash2 size={12} />
                          </button>
                        </div>
                      )}
                    </div>
                    {editingGroup === gid && (
                      <div className="friends-detail-remark-edit" style={{ padding: '4px 0 8px 28px' }}>
                        <input
                          className="ba-input"
                          value={editingGroupName}
                          onChange={(e) => setEditingGroupName(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') handleUpdateGroup(gid)
                            if (e.key === 'Escape') setEditingGroup(null)
                          }}
                          autoFocus
                        />
                        <button className="ba-btn ba-btn-primary" onClick={() => handleUpdateGroup(gid)} style={{ padding: '4px 8px' }}>
                          <Check size={14} />
                        </button>
                        <button className="ba-btn" onClick={() => setEditingGroup(null)} style={{ padding: '4px 8px' }}>
                          <X size={14} />
                        </button>
                      </div>
                    )}
                    {isExpanded && (
                      <div className="friend-group-list">
                        {groupFriends.map((f) => {
                          const status = onlineStatuses[f.friend_id] || 'offline'
                          const isSelected = selectedFriend?.friend_id === f.friend_id
                          return (
                            <div
                              key={f.friend_id}
                              className={`friend-item ${isSelected ? 'selected' : ''}`}
                              onClick={() => {
                                setSelectedFriend(f)
                                setRemarkValue(f.remark || '')
                                setEditingRemark(false)
                              }}
                            >
                              <div className="ba-avatar ba-avatar-sm">
                                {friendUserInfo[f.friend_id]?.avatar ? (
                                  <img src={friendUserInfo[f.friend_id].avatar} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                                ) : (
                                  (f.remark || friendUserInfo[f.friend_id]?.nickname || f.friend_id).charAt(0)
                                )}
                              </div>
                              <div className="friend-item-info">
                                <div className="friend-item-name">{f.remark || friendUserInfo[f.friend_id]?.nickname || f.friend_id}</div>
                                <div className="friend-item-status" style={{ color: statusColor(status) }}>
                                  <span className="status-dot" style={{ background: statusColor(status) }} />
                                  {statusLabel(status)}
                                </div>
                              </div>
                            </div>
                          )
                        })}
                        {groupFriends.length === 0 && gid === 'default' && (
                          <div className="friend-group-empty">暂无好友</div>
                        )}
                      </div>
                    )}
                  </div>
                )
              })}
              {friends.length === 0 && (
                <div className="knowledge-empty">
                  <UserPlus size={48} />
                  <p>暂无好友</p>
                  <p className="knowledge-empty-hint">点击右上角搜索并添加好友</p>
                </div>
              )}
            </div>

            {selectedFriend && (
              <div className="friends-detail ba-card">
                <div className="friends-detail-header">
                  <div className="ba-avatar">
                    {friendUserInfo[selectedFriend.friend_id]?.avatar ? (
                      <img src={friendUserInfo[selectedFriend.friend_id].avatar} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} />
                    ) : (
                      (selectedFriend.remark || friendUserInfo[selectedFriend.friend_id]?.nickname || selectedFriend.friend_id).charAt(0)
                    )}
                  </div>
                  <div className="friends-detail-info">
                    <div className="friends-detail-name">
                      {selectedFriend.remark || friendUserInfo[selectedFriend.friend_id]?.nickname || selectedFriend.friend_id}
                    </div>
                    <div className="friends-detail-id">ID: {selectedFriend.friend_id}</div>
                  </div>
                </div>

                <div className="friends-detail-section">
                  <div className="friends-detail-label">备注</div>
                  {editingRemark ? (
                    <div className="friends-detail-remark-edit">
                      <input
                        className="ba-input"
                        value={remarkValue}
                        onChange={(e) => setRemarkValue(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === 'Enter') handleUpdateRemark()
                          if (e.key === 'Escape') setEditingRemark(false)
                        }}
                        autoFocus
                      />
                      <button className="ba-btn ba-btn-primary" onClick={handleUpdateRemark} style={{ padding: '4px 8px' }}>
                        <Check size={14} />
                      </button>
                      <button className="ba-btn" onClick={() => setEditingRemark(false)} style={{ padding: '4px 8px' }}>
                        <X size={14} />
                      </button>
                    </div>
                  ) : (
                    <div
                      className="friends-detail-remark"
                      onClick={() => setEditingRemark(true)}
                      title="点击修改备注"
                    >
                      {selectedFriend.remark || '点击添加备注'}
                      <Edit3 size={12} className="friends-detail-remark-icon" />
                    </div>
                  )}
                </div>

                <div className="friends-detail-section">
                  <div className="friends-detail-label">分组</div>
                  {movingFriend === selectedFriend.friend_id ? (
                    <div className="friends-detail-group-select">
                      <button
                        className={`friends-detail-group-option ${!selectedFriend.group_id ? 'active' : ''}`}
                        onClick={() => handleMoveFriend(selectedFriend.friend_id, '')}
                      >
                        默认分组
                      </button>
                      {groups.map((g) => (
                        <button
                          key={g.id}
                          className={`friends-detail-group-option ${selectedFriend.group_id === g.id ? 'active' : ''}`}
                          onClick={() => handleMoveFriend(selectedFriend.friend_id, g.id)}
                        >
                          {g.name}
                        </button>
                      ))}
                      <button className="ba-btn" onClick={() => setMovingFriend(null)} style={{ marginTop: 4, padding: '4px 8px' }}>
                        取消
                      </button>
                    </div>
                  ) : (
                    <div
                      className="friends-detail-group"
                      onClick={() => setMovingFriend(selectedFriend.friend_id)}
                      title="点击更换分组"
                    >
                      {getGroupName(selectedFriend.group_id || 'default')}
                      <Edit3 size={12} className="friends-detail-remark-icon" />
                    </div>
                  )}
                </div>

                <div className="friends-detail-actions">
                  <button
                    className="ba-btn ba-btn-danger"
                    onClick={() => handleDeleteFriend(selectedFriend.friend_id)}
                  >
                    <Trash2 size={14} /> 删除好友
                  </button>
                  <button
                    className="ba-btn"
                    onClick={() => handleBlockUser(selectedFriend.friend_id)}
                    style={{ color: 'var(--ba-warning)' }}
                  >
                    <Ban size={14} /> 拉黑
                  </button>
                </div>
              </div>
            )}
            {!selectedFriend && (
              <div className="friends-detail-empty">
                <Users size={48} />
                <p>选择一个好友查看详情</p>
              </div>
            )}
          </div>
        )}

        {activeTab === 'requests' && (
          <div className="friends-requests">
            {pendingRequests.length === 0 && (
              <div className="knowledge-empty">
                <Bell size={48} />
                <p>暂无好友申请</p>
              </div>
            )}
            {pendingRequests.map((r) => (
              <div key={r.id} className="friend-request-card ba-card">
                <div className="friend-request-info">
                  <div className="ba-avatar ba-avatar-sm">
                    {(r.remark || r.from_user_id).charAt(0)}
                  </div>
                  <div>
                    <div className="friend-request-name">{r.remark || r.from_user_id}</div>
                    <div className="friend-request-message">{r.message || '请求添加你为好友'}</div>
                  </div>
                </div>
                <div className="friend-request-actions">
                  <button className="ba-btn ba-btn-primary" onClick={() => handleAcceptRequest(r.id)}>
                    <Check size={14} /> 接受
                  </button>
                  <button className="ba-btn" onClick={() => handleRejectRequest(r.id)}>
                    <X size={14} /> 拒绝
                  </button>
                </div>
              </div>
            ))}
          </div>
        )}

        {activeTab === 'blacklist' && (
          <div className="friends-blacklist">
            {blacklist.length === 0 && (
              <div className="knowledge-empty">
                <ShieldOff size={48} />
                <p>黑名单为空</p>
              </div>
            )}
            {blacklist.map((b) => (
              <div key={b.id} className="blacklist-card ba-card">
                <div className="blacklist-info">
                  <div className="ba-avatar ba-avatar-sm">{b.blocked_user_id.charAt(0)}</div>
                  <div>
                    <div className="blacklist-name">{b.blocked_user_id}</div>
                  </div>
                </div>
                <button
                  className="ba-btn"
                  onClick={() => handleUnblockUser(b.blocked_user_id)}
                  style={{ color: 'var(--ba-success)' }}
                >
                  <ShieldOff size={14} /> 取消拉黑
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {showSearch && (
        <div className="search-overlay" onClick={() => { setShowSearch(false); setShowAddMessage(null); setSearchResults([]) }}>
          <div className="search-panel ba-card" onClick={(e) => e.stopPropagation()}>
            <h3 style={{ marginBottom: 12 }}>搜索用户</h3>
            <div style={{ display: 'flex', gap: 8, marginBottom: 16 }}>
              <input
                className="ba-input"
                placeholder="输入用户名搜索..."
                value={searchKeyword}
                onChange={(e) => setSearchKeyword(e.target.value)}
                onKeyDown={(e) => e.key === 'Enter' && handleSearch()}
                autoFocus
              />
              <button className="ba-btn ba-btn-primary" onClick={handleSearch}>
                <Search size={16} />
              </button>
            </div>
            {searchResults.length > 0 && (
              <div className="search-results">
                {searchResults.map((u) => (
                  <div key={u.id} className="search-result-item">
                    <div className="ba-avatar ba-avatar-sm">{(u.nickname || u.id).charAt(0)}</div>
                    <div className="search-result-info">
                      <span className="search-result-name">{u.nickname || u.id}</span>
                      <span className="search-result-id">{u.username || u.id}</span>
                    </div>
                    {showAddMessage === u.id ? (
                      <div className="search-add-message">
                        <input
                          className="ba-input"
                          placeholder="验证消息..."
                          value={addMessage}
                          onChange={(e) => setAddMessage(e.target.value)}
                          onKeyDown={(e) => {
                            if (e.key === 'Enter') handleAddFriend(u.id)
                          }}
                          autoFocus
                        />
                        <button className="ba-btn ba-btn-primary" onClick={() => handleAddFriend(u.id)} style={{ padding: '4px 8px' }}>
                          <Check size={14} />
                        </button>
                        <button className="ba-btn" onClick={() => { setShowAddMessage(null); setAddMessage('') }} style={{ padding: '4px 8px' }}>
                          <X size={14} />
                        </button>
                      </div>
                    ) : (
                      <button
                        className="ba-btn ba-btn-primary"
                        onClick={() => setShowAddMessage(u.id)}
                        style={{ padding: '4px 12px', fontSize: 12 }}
                      >
                        <UserPlus size={14} />
                      </button>
                    )}
                  </div>
                ))}
              </div>
            )}
          </div>
        </div>
      )}

      {showNewGroup && (
        <div className="search-overlay" onClick={() => { setShowNewGroup(false); setNewGroupName('') }}>
          <div className="search-panel ba-card" onClick={(e) => e.stopPropagation()}>
            <h3 style={{ marginBottom: 12 }}>新建分组</h3>
            <div style={{ display: 'flex', gap: 8 }}>
              <input
                className="ba-input"
                placeholder="分组名称..."
                value={newGroupName}
                onChange={(e) => setNewGroupName(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') handleCreateGroup()
                }}
                autoFocus
              />
              <button className="ba-btn ba-btn-primary" onClick={handleCreateGroup}>
                创建
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
