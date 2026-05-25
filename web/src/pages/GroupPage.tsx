import { useState, useEffect, useRef, useCallback, useLayoutEffect, useMemo } from 'react'
import { Plus, Users, Crown, Shield, UserMinus, LogOut, Send, Paperclip, Smile, MoreVertical, Sparkles, FileText, RefreshCw, Camera } from 'lucide-react'
import type { ChatGroup } from '@/api/group'
import { createGroup, getGroup, getGroupMembers, inviteGroupMember, kickGroupMember, leaveGroup, joinGroup, updateGroupAvatar } from '@/api/group'
import { sendMessage, sendMediaMessage, uploadChatMedia, getChatHistory, toMediaUrl, getConversationList, resolveMessageType, forwardMessage } from '@/api/chat'
import { load, save } from '@/lib/store'
import { useAuth } from '@/hooks/useAuth'
import { useWebSocket } from '@/hooks/useWebSocket'
import Modal from '@/components/Modal'
import ChatBubble from '@/components/ChatBubble'
import type { Message } from '@/types'
import client from '@/api/client'
import './GroupPage.css'

const EMOJI_LIST = [
  '😀','😂','🤣','😊','😍','🥰','😘','😎','🤔','😏',
  '😢','😭','😤','🤯','😱','🥳','😴','🤮','👍','👎',
  '👏','🙏','💪','❤️','🔥','⭐','🎉','🎊','💯','✅',
  '❌','⚡','🌈','🌸','🍀','🐱','🐶','🦊','🐼','🤖',
]

interface MemberItem {
  userId: string
  username: string
  avatar: string
  role: 'owner' | 'admin' | 'member'
  joinedAt: string
}

function loadAllGroupMessages(): Record<string, Message[]> {
  try { return load<Record<string, Message[]>>('group_messages', {}) } catch { return {} }
}

function loadGroupMsgs(gid: string): Message[] {
  return loadAllGroupMessages()[gid] || []
}

function persistGroupMsgs(gid: string, msgs: Message[]) {
  try { const all = loadAllGroupMessages(); all[gid] = msgs; save('group_messages', all) } catch { /* */ }
}

function convertConversationToGroup(conv: any): ChatGroup {
  return {
    id: conv.chat_id || conv.id || '',
    name: conv.name || '群聊',
    avatar: conv.avatar || '',
    description: '',
    announcement: '',
    owner_id: '',
    member_ids: [],
    member_count: 0,
    created_at: conv.created_at || '',
  }
}

export default function GroupPage() {
  const { user } = useAuth()
  const membersRef = useRef<MemberItem[]>([])
  const sentMessagesRef = useRef<Map<string, { localId: string; timestamp: number }>>(new Map())
  const mediaUrlToLocalIdRef = useRef<Map<string, string>>(new Map())
  
  const wsBase = `${window.location.protocol === 'https:' ? 'wss' : 'ws'}://${window.location.host}/ws`
  const { send: wsSend, connected: wsConnected } = useWebSocket(wsBase, {
    onMessage: async (msg) => {
      if (msg.type === 'withdraw') {
        const data = msg.data as Record<string, unknown>
        const messageId = String(data.message_id || '')
        const chatId = String(data.chat_id || '')
        if (!messageId || !chatId) return

        const currentMsgs = loadGroupMsgs(chatId)
        const updated = currentMsgs.map((m) => {
          if (m.id === messageId) {
            return { ...m, content: '[消息已撤回]', messageType: 'withdrawn' as const }
          }
          return m
        })
        persistGroupMsgs(chatId, updated)
        if (activeGroupIdRef.current === chatId) setGroupMessages(updated)
        return
      }

      if (msg.type === 'message') {
        const incoming = (msg.data ?? msg.payload) as Record<string, unknown>
        if (incoming && incoming.chat_id) {
          const chatId = String(incoming.chat_id)
          const rawChatType = incoming.chat_type || 1
          const isGroupChat = rawChatType === 2 || chatId.startsWith('group_') || chatId.startsWith('group-')
          
          // 如果是群组消息，先检查是否在列表中，如果不在则获取群组信息并添加
          if (isGroupChat) {
            // 检查群组是否已经在列表中
            setGroups(prevGroups => {
              const existingGroup = prevGroups.find(g => g.id === chatId)
              if (!existingGroup) {
                // 群组不在列表中，获取群组信息并添加
                getGroup(chatId).then(groupInfo => {
                  if (groupInfo) {
                    setGroups(prev => {
                      const newGroup: ChatGroup = {
                        id: groupInfo.id,
                        name: groupInfo.name || '群组',
                        avatar: groupInfo.avatar || '',
                        description: groupInfo.description || '',
                        announcement: groupInfo.announcement || '',
                        owner_id: groupInfo.owner_id || '',
                        member_ids: groupInfo.member_ids || [],
                        member_count: groupInfo.member_count || 1,
                        created_at: groupInfo.created_at || new Date().toISOString(),
                      }
                      // 检查是否已经被添加（避免重复）
                      if (!prev.find(g => g.id === newGroup.id)) {
                        const updated = [newGroup, ...prev]
                        save('groups', updated)
                        return updated
                      }
                      return prev
                    })
                  }
                }).catch(() => {
                  // 获取群组信息失败，尝试刷新列表
                  refreshGroups()
                })
              }
              return prevGroups
            })
          }
          
          const currentMsgs = loadGroupMsgs(chatId)
          if (!currentMsgs.some((m) => m.id === incoming.id)) {
            let createdAt = ''
            const rawCreatedAt = incoming.created_at || incoming.createdAt
            if (typeof rawCreatedAt === 'string') {
              createdAt = rawCreatedAt
            } else if (typeof rawCreatedAt === 'object' && rawCreatedAt !== null) {
              const ts = rawCreatedAt as { seconds?: number; nanos?: number }
              if (ts.seconds) {
                createdAt = new Date(Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6)).toISOString()
              }
            }
            if (!createdAt) createdAt = new Date().toISOString()
            let newMsg: Message = {
              id: String(incoming.id || ''),
              chatId,
              senderId: String(incoming.sender_id || incoming.senderId || ''),
              senderName: String(incoming.sender_name || incoming.senderName || ''),
              senderAvatar: (String(incoming.sender_id || incoming.senderId || '') === user?.id && user?.avatar) || String(incoming.sender_avatar || incoming.senderAvatar || ''),
              content: String(incoming.content || ''),
              messageType: resolveMessageType(incoming.message_type || incoming.messageType),
              mediaUrl: toMediaUrl(String(incoming.media_url || incoming.mediaUrl || '')),
              mediaMeta: String(incoming.media_meta || incoming.mediaMeta || ''),
              createdAt,
              isBot: !!incoming.is_bot || !!incoming.isBot,
            }
            // 如果是系统消息，并且我们已经有成员列表，先修复一下用户名
            if (newMsg.messageType === 'system' && membersRef.current.length > 0) {
              const fixedContent = fixSystemMessageContentRef.current(newMsg.content, membersRef.current)
              if (fixedContent !== newMsg.content) {
                newMsg = { ...newMsg, content: fixedContent }
              }
            }
            
            const isSelfEcho = String(incoming.sender_id || incoming.senderId || '') === user?.id
            // 对于发送方，检查是否有本地临时消息需要替换
            if (isSelfEcho) {
              // 先尝试普通文本消息的 key
              let sentKey = `${chatId}:${newMsg.content.trim()}`
              let sentInfo = sentMessagesRef.current.get(sentKey)
              
              // 如果没找到，尝试媒体消息的 key
              if (!sentInfo && newMsg.mediaMeta) {
                sentKey = `${chatId}:${newMsg.content.trim()}:${newMsg.mediaMeta}`
                sentInfo = sentMessagesRef.current.get(sentKey)
              }
              
              // 如果还是没找到，尝试用 mediaUrl 匹配
              if (!sentInfo && newMsg.mediaUrl) {
                const rawMediaUrl = String(incoming.media_url || incoming.mediaUrl || '')
                const matchedLocalId = mediaUrlToLocalIdRef.current.get(rawMediaUrl)
                if (matchedLocalId) {
                  sentInfo = { localId: matchedLocalId, timestamp: Date.now() }
                  sentKey = `mediaUrl:${rawMediaUrl}`
                }
              }
              
              if (sentInfo) {
                const localIdx = currentMsgs.findIndex((m) => m.id === sentInfo.localId)
                if (localIdx !== -1) {
                  const updatedMsgs = currentMsgs.map((m) => m.id === sentInfo.localId ? newMsg : m)
                  const sorted = updatedMsgs.sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
                  persistGroupMsgs(chatId, sorted)
                  if (activeGroupIdRef.current === chatId) setGroupMessages(sorted)
                  sentMessagesRef.current.delete(sentKey)
                  if (sentKey.startsWith('mediaUrl:')) {
                    mediaUrlToLocalIdRef.current.delete(sentKey.replace('mediaUrl:', ''))
                  }
                  return
                }
                sentMessagesRef.current.delete(sentKey)
              }
            }
            
            // 直接添加新消息并排序
            const updated = [...currentMsgs, newMsg]
            const sorted = updated.sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
            persistGroupMsgs(chatId, sorted)
            if (activeGroupIdRef.current === chatId) setGroupMessages(sorted)
          }
        }
      }
    },
  })
  const [groups, setGroups] = useState<ChatGroup[]>(() => load('groups', []).filter((g: ChatGroup) => !g.id.startsWith('local-')))
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  const refreshGroups = useCallback(async () => {
    setRefreshing(true)
    try {
      const conversations = await getConversationList(1, 100)
      if (conversations && Array.isArray(conversations)) {
        const newNameMap: Record<string, string> = {}
        for (const conv of conversations) {
          const chatId = String(conv.chat_id || conv.id || '')
          const chatType = conv.chat_type || conv.type
          const name = conv.name || conv.newName || ''
          if (chatType === 1 && chatId.startsWith('private_')) {
            const parts = chatId.split('_')
            if (parts.length === 3) {
              const myId = String(user?.id || '')
              const otherUserId = parts[1] === myId ? parts[2] : parts[1]
              if (name && otherUserId) {
                newNameMap[otherUserId] = name
              }
            }
          }
        }
        if (Object.keys(newNameMap).length > 0) {
          setUserNameMap(prev => ({ ...prev, ...newNameMap }))
        }

        const groupConvs = conversations.filter((c: any) => {
          const chatType = c.chat_type || c.type
          const chatId = c.chat_id || c.id || ''
          return chatType === 2 || chatId.startsWith('group-') || chatId.startsWith('group_') || (c.name && c.name !== '')
        })
        
        // 获取每个群组的详细信息
        const groupPromises = groupConvs.map((conv: any) => {
          const chatId = String(conv.chat_id || conv.id || '')
          return getGroup(chatId).catch(() => null)
        })
        
        const groupInfos = await Promise.all(groupPromises)
        
        // 合并信息
        const newGroups = groupConvs.map((conv: any, index: number) => {
          const groupFromList = convertConversationToGroup(conv)
          const groupDetail = groupInfos[index]
          
          if (groupDetail) {
            return {
              ...groupFromList,
              name: groupDetail.name || groupFromList.name,
              avatar: groupDetail.avatar || groupFromList.avatar,
              member_count: groupDetail.member_count || groupFromList.member_count,
            }
          }
          return groupFromList
        }).filter(g => g.id)
        
        if (newGroups.length > 0) {
          setGroups(newGroups)
          save('groups', newGroups)
        }
      }
    } catch {
      // ignore
    } finally {
      setRefreshing(false)
    }
  }, [])

  useEffect(() => {
    refreshGroups()
    const interval = setInterval(refreshGroups, 30000) // 每30秒刷新一次
    return () => clearInterval(interval)
  }, [refreshGroups])
  const [members, setMembers] = useState<MemberItem[]>([])
  // 同步 members 到 ref
  useEffect(() => {
    membersRef.current = members
  }, [members])
  const [membersError, setMembersError] = useState('')
  const [showCreate, setShowCreate] = useState(false)
  const [showAddMember, setShowAddMember] = useState(false)
  const [newGroupName, setNewGroupName] = useState('')
  const [newGroupDesc, setNewGroupDesc] = useState('')
  const [addMemberId, setAddMemberId] = useState('')
  const [addMemberError, setAddMemberError] = useState('')
  const [joinGroupId, setJoinGroupId] = useState('')
  const [joinGroupError, setJoinGroupError] = useState('')
  const [joinLoading, setJoinLoading] = useState(false)
  const [createLoading, setCreateLoading] = useState(false)
  const [showJoin, setShowJoin] = useState(false)
  const [groupMessages, setGroupMessages] = useState<Message[]>([])
  const [userNameMap, setUserNameMap] = useState<Record<string, string>>({})
  const mappedSenderIdsRef = useRef<Set<string>>(new Set())
  const [inputText, setInputText] = useState('')
  const [showEmoji, setShowEmoji] = useState(false)
  const [emojiPos, setEmojiPos] = useState<{ top: number; left: number } | null>(null)
  const [showMenu, setShowMenu] = useState(false)
  const [summary, setSummary] = useState('')
  const [summaryLoading, setSummaryLoading] = useState(false)
  const [showSummary, setShowSummary] = useState(false)
  const [rightTab, setRightTab] = useState<'chat' | 'members'>('chat')
  const [showForwardModal, setShowForwardModal] = useState(false)
  const [forwardingMessage, setForwardingMessage] = useState<Message | null>(null)
  const [forwardChatId, setForwardChatId] = useState<string | null>(null)
  const [forwardChatType, setForwardChatType] = useState<'private' | 'group' | 'bot'>('private')
  const [forwardChats, setForwardChats] = useState<Array<{ id: string; name: string; type: 'private' | 'group' | 'bot'; avatar: string; lastMessage?: string }>>([])
  const emojiBtnRef = useRef<HTMLButtonElement>(null)
  const emojiPickerRef = useRef<HTMLDivElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const messagesContainerRef = useRef<HTMLDivElement>(null)
  const scrollPositionRef = useRef(0) // 保存滚动位置
  const fileRef = useRef<HTMLInputElement>(null)
  const avatarRef = useRef<HTMLInputElement>(null)
  const activeGroupIdRef = useRef<string | null>(null)

  const selectedGroup = groups.find((g) => g.id === selectedGroupId) || null

  useEffect(() => { save('groups', groups) }, [groups])

  useEffect(() => {
    const handleClick = (e: MouseEvent) => {
      if (menuRef.current && !menuRef.current.contains(e.target as Node)) setShowMenu(false)
      if (emojiPickerRef.current && !emojiPickerRef.current.contains(e.target as Node) && emojiBtnRef.current && !emojiBtnRef.current.contains(e.target as Node)) { setShowEmoji(false); setEmojiPos(null) }
    }
    document.addEventListener('mousedown', handleClick)
    return () => document.removeEventListener('mousedown', handleClick)
  }, [])

  // 辅助函数：替换系统消息中的用户ID为用户名
  const fixSystemMessageContent = useCallback((content: string, memberList: MemberItem[]): string => {
    for (const [uid, uname] of Object.entries(userNameMap)) {
      if (uname && content.includes(uid)) {
        content = content.replace(new RegExp(`(?<!\\d)${uid.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?!\\d)`, 'g'), uname)
      }
    }
    for (const member of memberList) {
      if (member.username && member.userId && content.includes(member.userId)) {
        content = content.replace(new RegExp(`(?<!\\d)${member.userId.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(?!\\d)`, 'g'), member.username)
      }
    }
    return content
  }, [userNameMap])

  // 用 useMemo 在渲染时计算修复后的消息，不修改 state
  // 把 rightTab 加入依赖，确保标签切换时 displayMessages 引用变化，触发滚动
  const displayMessages = useMemo(() => {
    if (members.length === 0 && Object.keys(userNameMap).length === 0) return groupMessages
    return groupMessages.map(msg => {
      if (msg.messageType !== 'system') return msg
      const fixedContent = fixSystemMessageContent(msg.content, members)
      if (fixedContent === msg.content) return msg
      return { ...msg, content: fixedContent }
    })
  }, [groupMessages, members, userNameMap, fixSystemMessageContent, rightTab])

  // 用 ref 保存函数，避免 WebSocket 依赖问题
  const fixSystemMessageContentRef = useRef(fixSystemMessageContent)
  useEffect(() => {
    fixSystemMessageContentRef.current = fixSystemMessageContent
  }, [fixSystemMessageContent])

  const loadMembers = useCallback(async (groupId: string) => {
    setMembersError('')
    try {
      const list = await getGroupMembers(groupId)
      setMembers(list as MemberItem[])
      const memberNameMap: Record<string, string> = {}
      for (const member of list as MemberItem[]) {
        if (member.userId && member.username) {
          memberNameMap[member.userId] = member.username
        }
      }
      if (Object.keys(memberNameMap).length > 0) {
        setUserNameMap(prev => ({ ...prev, ...memberNameMap }))
      }
      if (list.length === 0) setMembersError('暂无成员')
    } catch {
      setMembers([])
      setMembersError('无法加载成员列表，服务暂时不可用')
    }
  }, [])

  useEffect(() => {
    const updates: Record<string, string> = {}
    for (const msg of groupMessages) {
      if (msg.senderId && msg.senderName && msg.senderName !== '用户' && msg.senderName !== '' && msg.senderId !== 'system' && !mappedSenderIdsRef.current.has(msg.senderId)) {
        updates[msg.senderId] = msg.senderName
        mappedSenderIdsRef.current.add(msg.senderId)
      }
    }
    if (user?.id && !mappedSenderIdsRef.current.has(user.id)) {
      const myName = user.nickname || user.username
      if (myName) {
        updates[user.id] = myName
        mappedSenderIdsRef.current.add(user.id)
      }
    }
    if (Object.keys(updates).length > 0) {
      setUserNameMap(prev => ({ ...prev, ...updates }))
    }
  }, [groupMessages, user])

  const fetchGroupMessages = useCallback(async (groupId: string) => {
    try {
      const data = await getChatHistory(groupId)
      if (Array.isArray(data) && data.length > 0) {
        const localMsgs = loadGroupMsgs(groupId)
        const localMap = new Map<string, Message>()
        for (const lm of localMsgs) localMap.set(lm.id, lm)
        
        const remoteIds = new Set(data.map((m: Record<string, unknown>) => String(m.id || '')))
        const merged = [
          ...data.map((m: Record<string, unknown>) => {
            let createdAt = ''
            const rawCreatedAt = m.created_at || m.createdAt
            if (rawCreatedAt) {
              if (typeof rawCreatedAt === 'string') {
                createdAt = rawCreatedAt
              } else if (typeof rawCreatedAt === 'object') {
                const ts = rawCreatedAt as { seconds?: number; nanos?: number }
                if (ts.seconds) {
                  createdAt = new Date(Number(ts.seconds) * 1000 + Math.floor((ts.nanos || 0) / 1e6)).toISOString()
                }
              }
            }
            if (!createdAt) createdAt = new Date().toISOString()
            const id = String(m.id || '')
            const remoteMsg: Message = {
              id,
              chatId: String(m.chat_id || m.chatId || groupId),
              senderId: String(m.sender_id || m.senderId || ''),
              senderName: String(m.sender_name || m.senderName || ''),
              senderAvatar: (String(m.sender_id || m.senderId || '') === user?.id && user?.avatar) || String(m.sender_avatar || m.senderAvatar || ''),
              content: String(m.content || ''),
              messageType: resolveMessageType(m.message_type || m.messageType),
              mediaUrl: toMediaUrl(String(m.media_url || m.mediaUrl || '')),
              mediaMeta: String(m.media_meta || m.mediaMeta || ''),
              createdAt,
              isBot: !!m.is_bot || !!m.isBot,
            }
            // 合并：如果远程消息缺少 mediaUrl/mediaMeta 但本地有，保留本地的
            const existing = localMap.get(id)
            if (existing) {
              return {
                ...remoteMsg,
                mediaUrl: remoteMsg.mediaUrl || existing.mediaUrl,
                mediaMeta: remoteMsg.mediaMeta || existing.mediaMeta,
                messageType: (remoteMsg.messageType === 'text' && existing.messageType !== 'text') ? existing.messageType : remoteMsg.messageType,
              }
            }
            return remoteMsg
          }),
          ...localMsgs.filter((lm) => !remoteIds.has(lm.id) && !lm.id.startsWith('local-')),
        ]
        const unique = new Map<string, Message>()
        for (const m of merged) unique.set(m.id, m)
        const sorted = Array.from(unique.values()).sort((a, b) => new Date(a.createdAt).getTime() - new Date(b.createdAt).getTime())
        persistGroupMsgs(groupId, sorted)
        if (activeGroupIdRef.current === groupId) setGroupMessages(sorted)
      }
    } catch { /* keep local */ }
  }, [])

  useEffect(() => {
    if (selectedGroupId) {
      activeGroupIdRef.current = selectedGroupId
      const rawMsgs = loadGroupMsgs(selectedGroupId)
      // 确保 mediaUrl 被正确处理
      const fixedMsgs = rawMsgs.map((msg) => ({
        ...msg,
        mediaUrl: msg.mediaUrl ? toMediaUrl(msg.mediaUrl) : undefined
      }))
      setGroupMessages(fixedMsgs)
      loadMembers(selectedGroupId)
      fetchGroupMessages(selectedGroupId)
    } else {
      activeGroupIdRef.current = null
      setGroupMessages([])
      setMembers([])
    }
  }, [selectedGroupId, loadMembers, fetchGroupMessages])

  const lastGroupIdRef = useRef<string | null>(null)

  // 选择新群组时，滚动到底部
  useEffect(() => {
    if (selectedGroupId && selectedGroupId !== lastGroupIdRef.current) {
      lastGroupIdRef.current = selectedGroupId
      const doScroll = () => {
        if (messagesContainerRef.current) {
          messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight
        }
      }
      setTimeout(doScroll, 50)
      setTimeout(doScroll, 150)
      setTimeout(doScroll, 300)
    }
  }, [selectedGroupId])

  // 收到新消息时，如果在底部，保持在底部
  useEffect(() => {
    if (messagesContainerRef.current) {
      const isNearBottom = messagesContainerRef.current.scrollHeight - messagesContainerRef.current.scrollTop - messagesContainerRef.current.clientHeight < 100
      if (isNearBottom) {
        const doScroll = () => {
          if (messagesContainerRef.current) {
            messagesContainerRef.current.scrollTop = messagesContainerRef.current.scrollHeight
          }
        }
        setTimeout(doScroll, 50)
      }
    }
  }, [displayMessages.length])

  const handleCreate = async () => {
    if (!newGroupName.trim()) return
    setCreateLoading(true)
    try {
      const group = await createGroup(newGroupName.trim(), newGroupDesc.trim())
      if (group && group.id) {
        setGroups((prev) => [group, ...prev])
        setSelectedGroupId(group.id)
        setShowCreate(false)
        setNewGroupName('')
        setNewGroupDesc('')
      }
    } catch (e: unknown) {
      const msg = e instanceof Error ? e.message : '创建群组失败'
      alert(msg)
    } finally {
      setCreateLoading(false)
    }
  }

  const handleAddMember = async () => {
    if (!selectedGroup || !addMemberId.trim()) return
    setAddMemberError('')
    try {
      await inviteGroupMember(selectedGroup.id, [addMemberId.trim()])
      loadMembers(selectedGroup.id)
      const updatedGroup = await getGroup(selectedGroup.id)
      if (updatedGroup) setGroups((prev) => prev.map((g) => g.id === selectedGroup.id ? updatedGroup : g))
      setShowAddMember(false)
      setAddMemberId('')
      await refreshGroups() // 刷新群组列表，让被邀请的用户也能看到
    } catch {
      setAddMemberError('邀请失败，请检查用户ID是否正确')
    }
  }

  const handleRemoveMember = async (userId: string) => {
    if (!selectedGroup) return
    try { 
      await kickGroupMember(selectedGroup.id, userId); 
      loadMembers(selectedGroup.id);
      // 更新群组列表的成员数
      const updatedGroup = await getGroup(selectedGroup.id)
      if (updatedGroup) setGroups((prev) => prev.map((g) => g.id === selectedGroup.id ? updatedGroup : g))
    } catch { /* */ }
  }

  const handleLeave = async () => {
    if (!selectedGroup) return
    const gid = selectedGroup.id
    setGroups((prev) => prev.filter((g) => g.id !== gid))
    setSelectedGroupId(null)
    try { await leaveGroup(gid) } catch { /* */ }
  }

  const handleJoin = async () => {
    if (!joinGroupId.trim()) return
    const gid = joinGroupId.trim()
    setJoinGroupError('')
    setJoinLoading(true)

    const existing = groups.find((g) => g.id === gid)
    if (existing) {
      setSelectedGroupId(existing.id)
      setShowJoin(false)
      setJoinGroupId('')
      setJoinLoading(false)
      return
    }

    try {
      await joinGroup(gid)
      const group = await getGroup(gid)
      if (group && group.id) {
        setGroups((prev) => [group, ...prev])
        setSelectedGroupId(group.id)
        setShowJoin(false)
        setJoinGroupId('')
      } else {
        setJoinGroupError('加入群组失败：群组不存在或服务器未返回数据')
      }
    } catch {
      setJoinGroupError('群组不存在或加入失败，请检查群组ID')
    } finally {
      setJoinLoading(false)
    }
  }

  const handleGroupSend = async () => {
    const text = inputText.trim()
    if (!text || !selectedGroup) return
    const gid = selectedGroup.id
    const localId = `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const localMsg: Message = { id: localId, chatId: gid, senderId: user?.id || '', senderName: user?.nickname || '我', senderAvatar: user?.avatar || '', content: text, messageType: 'text', createdAt: new Date().toISOString() }
    
    // 保存到 sentMessagesRef，用于后续去重
    const sentKey = `${gid}:${text.trim()}`
    sentMessagesRef.current.set(sentKey, { localId, timestamp: Date.now() })
    
    const current = loadGroupMsgs(gid)
    const updated = [...current, localMsg]
    persistGroupMsgs(gid, updated)
    if (activeGroupIdRef.current === gid) setGroupMessages(updated)
    setInputText('')
    if (wsConnected) {
      wsSend({
        type: 'message',
        data: {
          chat_id: gid,
          content: text,
          chat_type: 2,
          message_type: 1,
        },
      })
    } else {
      try { await sendMessage(gid, text, 'text', undefined, 'group') } catch { /* */ }
    }
  }

  const handleGroupUpload = async (file: File) => {
    if (!selectedGroup) return
    const gid = selectedGroup.id
    const t = file.type || ''
    const mediaType = t.startsWith('image/') ? 'image' : t.startsWith('video/') ? 'video' : t.startsWith('audio/') ? 'voice' : 'file'
    const localId = `local-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    const content = mediaType === 'image' ? '' : `[${mediaType}] ${file.name}`
    const localMsg: Message = { id: localId, chatId: gid, senderId: user?.id || '', senderName: user?.nickname || '我', senderAvatar: user?.avatar || '', content, messageType: mediaType, mediaUrl: mediaType === 'image' ? URL.createObjectURL(file) : undefined, mediaMeta: file.name, createdAt: new Date().toISOString() }
    
    // 保存到 sentMessagesRef，用于后续去重
    const sentKey = `${gid}:${content.trim()}:${file.name}`
    sentMessagesRef.current.set(sentKey, { localId, timestamp: Date.now() })
    
    // 立即更新 UI 显示！
    const current = loadGroupMsgs(gid)
    const updated = [...current, localMsg]
    persistGroupMsgs(gid, updated)
    if (activeGroupIdRef.current === gid) setGroupMessages(updated)
    
    try {
      const res = await uploadChatMedia(gid, file)
      if (res.url) { 
        // 保存 remoteUrl → localId 映射，用于 WS 消息去重
        mediaUrlToLocalIdRef.current.set(res.url, localId)
        sendMediaMessage(gid, '', res.url, mediaType, file.name, 'group').catch(() => {}) 
      }
    } catch {
      alert('上传文件失败，请重试')
    }
  }

  const toggleEmoji = () => {
    if (showEmoji) { setShowEmoji(false); setEmojiPos(null); return }
    if (emojiBtnRef.current) {
      const rect = emojiBtnRef.current.getBoundingClientRect()
      setEmojiPos({ top: rect.top - 8, left: rect.left + rect.width / 2 })
    }
    setShowEmoji(true)
  }

  const handleForwardMessage = async (message: Message) => {
    if (message.id.startsWith('local-')) {
      alert('请等待消息发送完成后再转发')
      return
    }
    setForwardingMessage(message)
    setForwardChatId(null)
    setForwardChatType('private')
    try {
      const convData = await getConversationList()
      const convs = (convData as any)?.conversations || convData || []
      const chatList: Array<{ id: string; name: string; type: 'private' | 'group' | 'bot'; avatar: string; lastMessage?: string }> = []
      for (const conv of convs) {
        const chatId = String(conv.chat_id || conv.id || '')
        if (!chatId || chatId === selectedGroupId) continue
        const rawType = conv.chat_type || conv.type
        const isGroup = rawType === 2 || chatId.startsWith('group_') || chatId.startsWith('group-')
        chatList.push({
          id: chatId,
          name: conv.name || (isGroup ? '群组' : '用户'),
          type: isGroup ? 'group' : 'private',
          avatar: conv.avatar || '',
          lastMessage: conv.last_message || '',
        })
      }
      setForwardChats(chatList)
    } catch {
      setForwardChats([])
    }
    setShowForwardModal(true)
  }

  const handleConfirmForward = async () => {
    if (!forwardingMessage || !forwardChatId) return
    if (forwardingMessage.id.startsWith('local-')) {
      alert('请等待消息发送完成后再转发')
      return
    }
    setShowForwardModal(false)
    setForwardingMessage(null)
    try {
      await forwardMessage(forwardingMessage.id, [forwardChatId], forwardChatType === 'group' ? 2 : 1)
    } catch {
      alert('转发失败')
    }
  }

  const handleSummarize = async () => {
    if (!selectedGroupId) return
    setShowMenu(false)
    setSummaryLoading(true)
    setShowSummary(true)
    setSummary('')
    try {
      const res = await client.post('/summary/messages', { chat_id: selectedGroupId, chat_type: 2, include_todos: true, include_candidates: false })
      const d = res.data as Record<string, unknown>
      const inner = (d.data || d) as Record<string, unknown>
      setSummary(String(inner.summary || inner.message || '无法生成摘要'))
    } catch { setSummary('摘要生成失败') }
    finally { setSummaryLoading(false) }
  }

  const handleGroupAvatarUpload = async (file: File) => {
    if (!selectedGroup) return
    try {
      const url = await updateGroupAvatar(selectedGroup.id, file)
      if (url) {
        setGroups(prev => prev.map(g => g.id === selectedGroup.id ? { ...g, avatar: url } : g))
      }
    } catch {
      alert('上传群头像失败')
    }
    if (avatarRef.current) avatarRef.current.value = ''
  }

  const roleIcon = (role: string) => {
    if (role === 'owner') return <Crown size={14} className="role-owner" />
    if (role === 'admin') return <Shield size={14} className="role-admin" />
    return null
  }

  const displayMemberCount = selectedGroup ? Math.max(selectedGroup.member_count, members.length) : 0

  return (
    <div className="group-page">
      <div className="group-list-panel">
        <div className="group-list-header">
          <h2>群组</h2>
          <div style={{ display: 'flex', gap: 4 }}>
            <button className="ba-btn ba-btn-secondary" onClick={() => { setShowJoin(true); setJoinGroupError('') }} style={{ padding: '6px 14px', fontSize: 13 }}>加入</button>
            <button className="ba-btn ba-btn-primary" onClick={() => setShowCreate(true)} style={{ padding: '6px 14px', fontSize: 13 }}><Plus size={16} /> 创建</button>
            <button className="ba-btn ba-btn-secondary" onClick={refreshGroups} disabled={refreshing} style={{ padding: '6px 14px', fontSize: 13 }}>
              <RefreshCw size={16} className={refreshing ? 'spin' : ''} />
            </button>
          </div>
        </div>
        <div className="group-list-items">
          {groups.map((group) => (
            <div key={group.id} className={`group-list-item ba-card ${selectedGroupId === group.id ? 'active' : ''}`} onClick={() => setSelectedGroupId(group.id)}>
              <div className="ba-avatar">{group.avatar ? <img src={toMediaUrl(group.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} /> : <Users size={20} />}</div>
              <div className="group-list-item-info">
                <div className="group-list-item-name">{group.name}</div>
                <div className="group-list-item-desc">
                  {group.description || '暂无描述'}
                  <span style={{ marginLeft: 8, color: 'var(--ba-blue)', fontSize: 11 }}>{group.member_count}人</span>
                </div>
              </div>
            </div>
          ))}
          {groups.length === 0 && <div className="chat-list-empty">暂无群组</div>}
        </div>
      </div>

      <div className="group-detail-panel">
        {selectedGroup ? (
          <>
            <div className="group-detail-header">
              <div className="group-detail-title">
                <div className="ba-avatar ba-avatar-lg" style={{ position: 'relative', cursor: 'pointer' }} onClick={() => avatarRef.current?.click()}>
                  {selectedGroup.avatar ? <img src={toMediaUrl(selectedGroup.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} /> : <Users size={24} />}
                  <div style={{ 
                    position: 'absolute', 
                    bottom: -4, 
                    right: -4, 
                    background: 'var(--ba-blue)', 
                    borderRadius: '50%', 
                    width: 24, 
                    height: 24, 
                    display: 'flex', 
                    alignItems: 'center', 
                    justifyContent: 'center',
                    border: '2px solid var(--ba-bg-primary)',
                    boxShadow: '0 2px 8px rgba(0,0,0,0.2)'
                  }}>
                    <Camera size={12} color="white" />
                  </div>
                </div>
                <input ref={avatarRef} type="file" accept="image/*" hidden onChange={() => { const f = avatarRef.current?.files?.[0]; if (f) handleGroupAvatarUpload(f) }} />
                <div>
                  <h3>{selectedGroup.name}</h3>
                  <p className="group-detail-desc">{displayMemberCount}人 | ID: {selectedGroup.id}</p>
                </div>
              </div>
              <div className="group-detail-actions" ref={menuRef} style={{ position: 'relative' }}>
                <button className="ba-btn ba-btn-secondary" onClick={() => { setShowAddMember(true); setAddMemberError('') }}><Plus size={16} /> 邀请</button>
                <button className="ba-btn ba-btn-danger" onClick={handleLeave}><LogOut size={16} /> 退出</button>
                <button className="header-action-btn" onClick={() => setShowMenu(!showMenu)}><MoreVertical size={18} /></button>
                {showMenu && (
                  <div className="header-dropdown-menu">
                    <button className="header-dropdown-item" onClick={handleSummarize}><Sparkles size={16} /> 生成摘要</button>
                    <button className="header-dropdown-item" onClick={() => setShowMenu(false)}><FileText size={16} /> 聊天记录</button>
                  </div>
                )}
              </div>
            </div>

            <div className="group-tabs">
              <button className={`group-tab ${rightTab === 'chat' ? 'active' : ''}`} onClick={() => setRightTab('chat')}>💬 群聊</button>
              <button className={`group-tab ${rightTab === 'members' ? 'active' : ''}`} onClick={() => setRightTab('members')}>👥 成员 ({displayMemberCount})</button>
            </div>

              {/* 完全复制 ChatPage 的结构！！！ */}
              {rightTab === 'members' ? (
                <div className="group-members">
                {membersError ? (
                  <div className="chat-list-empty" style={{ padding: '20px 0', color: 'var(--ba-text-light)' }}>
                    {membersError}
                  </div>
                ) : members.length > 0 ? (
                  <div className="group-members-grid">
                    {members.map((member) => (
                      <div key={member.userId} className="group-member-card ba-card">
                        <div className="ba-avatar ba-avatar-sm">{member.avatar ? <img src={toMediaUrl(member.avatar)} alt="" style={{ width: '100%', height: '100%', borderRadius: '50%', objectFit: 'cover' }} /> : (member.username || member.userId).charAt(0) || '?'}</div>
                        <div className="group-member-info">
                          <div className="group-member-name">{member.username || `用户 ${member.userId.slice(0, 8)}`} {roleIcon(member.role)}</div>
                          <div className="group-member-role">{member.role === 'owner' ? '群主' : member.role === 'admin' ? '管理员' : '成员'}</div>
                        </div>
                        {member.role !== 'owner' && (
                          <button className="group-member-remove" onClick={() => handleRemoveMember(member.userId)} title="移除"><UserMinus size={14} /></button>
                        )}
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="chat-list-empty" style={{ padding: '20px 0' }}>
                    暂无成员，点击"邀请"添加
                  </div>
                )}
              </div>
              ) : (
                <>
                  {/* 完全用 ChatPage 的消息区域结构！！！ */}
                  <div className="chat-window-messages" ref={messagesContainerRef}>
                    {displayMessages.map((msg, i) => (
                      <ChatBubble key={msg.id || i} message={msg} isOwn={msg.senderId === user?.id} showAvatar={i === 0 || displayMessages[i - 1]?.senderId !== msg.senderId} onForward={handleForwardMessage} />
                    ))}
                    <div ref={messagesEndRef} />
                  </div>
                  <div className="group-chat-input">
                    <button className="input-action-btn" onClick={() => fileRef.current?.click()} title="上传文件"><Paperclip size={18} /></button>
                    <input ref={fileRef} type="file" hidden onChange={() => { const f = fileRef.current?.files?.[0]; if (f) handleGroupUpload(f); if (fileRef.current) fileRef.current.value = '' }} />
                    <button ref={emojiBtnRef} className={`input-action-btn ${showEmoji ? 'active' : ''}`} onClick={toggleEmoji} title="表情"><Smile size={18} /></button>
                    <input className="ba-input group-chat-input-field" placeholder="输入群消息..." value={inputText} onChange={(e) => setInputText(e.target.value)} onKeyDown={(e) => e.key === 'Enter' && !e.shiftKey && handleGroupSend()} />
                    <button className="message-input-send" onClick={handleGroupSend} disabled={!inputText.trim()}><Send size={18} /></button>
                  </div>
                  {showEmoji && emojiPos && (
                    <div ref={emojiPickerRef} className="emoji-picker" style={{ position: 'fixed', transform: 'translate(-50%, -100%)', top: emojiPos.top, left: emojiPos.left }}>
                      {EMOJI_LIST.map((emoji) => (
                        <button key={emoji} className="emoji-item" onClick={() => setInputText((prev) => prev + emoji)}>{emoji}</button>
                      ))}
                    </div>
                  )}
                </>
              )}
          </>
        ) : (
          <div className="chat-window-empty">
            <div className="empty-icon">👥</div>
            <div className="empty-text">选择一个群组查看详情</div>
          </div>
        )}
      </div>

      <Modal open={showSummary} onClose={() => setShowSummary(false)} title="群聊摘要">
        <div style={{ minHeight: 100 }}>
          {summaryLoading ? <p style={{ textAlign: 'center', color: 'var(--ba-text-light)' }}>正在生成摘要...</p> : <p style={{ lineHeight: 1.8, whiteSpace: 'pre-wrap' }}>{summary}</p>}
        </div>
      </Modal>

      <Modal open={showCreate} onClose={() => setShowCreate(false)} title="创建群组">
        <div className="new-chat-form">
          <div className="login-field"><label>群组名称</label><input className="ba-input" placeholder="输入群组名称" value={newGroupName} onChange={(e) => setNewGroupName(e.target.value)} /></div>
          <div className="login-field"><label>群组描述</label><textarea className="ba-input" placeholder="输入群组描述（选填）" rows={3} value={newGroupDesc} onChange={(e) => setNewGroupDesc(e.target.value)} /></div>
          <button className="ba-btn ba-btn-primary" onClick={handleCreate} disabled={createLoading} style={{ marginTop: 16, width: '100%' }}>
            {createLoading ? '创建中...' : '创建'}
          </button>
        </div>
      </Modal>

      <Modal open={showAddMember} onClose={() => { setShowAddMember(false); setAddMemberError('') }} title="邀请成员">
        <div className="new-chat-form">
          <div className="login-field"><label>用户ID</label><input className="ba-input" placeholder="输入用户ID" value={addMemberId} onChange={(e) => { setAddMemberId(e.target.value); setAddMemberError('') }} onKeyDown={(e) => e.key === 'Enter' && handleAddMember()} />
            {addMemberError && <p style={{ color: 'var(--ba-accent)', fontSize: 12, marginTop: 4 }}>{addMemberError}</p>}
          </div>
          <button className="ba-btn ba-btn-primary" onClick={handleAddMember} style={{ marginTop: 16, width: '100%' }}>邀请</button>
        </div>
      </Modal>

      <Modal open={showJoin} onClose={() => { setShowJoin(false); setJoinGroupError(''); setJoinGroupId('') }} title="加入群组">
        <div className="new-chat-form">
          <div className="login-field"><label>群组ID</label><input className="ba-input" placeholder="输入群组ID" value={joinGroupId} onChange={(e) => { setJoinGroupId(e.target.value); setJoinGroupError('') }} onKeyDown={(e) => e.key === 'Enter' && handleJoin()} />
            {joinGroupError && <p style={{ color: 'var(--ba-accent)', fontSize: 12, marginTop: 4 }}>{joinGroupError}</p>}
          </div>
          <button className="ba-btn ba-btn-primary" onClick={handleJoin} disabled={joinLoading} style={{ marginTop: 16, width: '100%' }}>
            {joinLoading ? '加入中...' : '加入'}
          </button>
        </div>
      </Modal>

      <Modal open={showForwardModal} onClose={() => setShowForwardModal(false)} title="转发消息" width={800}>
        <div style={{ display: 'flex', gap: 20, minHeight: 400 }}>
          <div style={{ width: '40%', borderRight: '1px solid var(--ba-border)', paddingRight: 20 }}>
            <div style={{ fontSize: 14, color: 'var(--ba-text-light)', marginBottom: 12 }}>要转发的消息：</div>
            <div style={{ background: 'var(--ba-bg-tertiary)', padding: 16, borderRadius: 8, border: '1px solid var(--ba-border)' }}>
              <div style={{ fontSize: 14, fontWeight: 600, marginBottom: 8 }}>{forwardingMessage?.senderName}</div>
              {forwardingMessage?.messageType !== 'text' && forwardingMessage?.mediaUrl && (
                <div style={{ marginBottom: 8 }}>
                  {forwardingMessage.messageType === 'image' && (
                    <img src={toMediaUrl(forwardingMessage.mediaUrl)} alt="" style={{ maxWidth: '100%', maxHeight: 120, borderRadius: 6, objectFit: 'cover' }} />
                  )}
                  {forwardingMessage.messageType === 'file' && (
                    <div style={{ fontSize: 12, color: 'var(--ba-text-light)' }}>📎 {forwardingMessage.mediaMeta || '文件'}</div>
                  )}
                </div>
              )}
              <div style={{ fontSize: 14, color: 'var(--ba-text)' }}>{forwardingMessage?.content}</div>
            </div>
          </div>
          <div style={{ width: '60%' }}>
            <div style={{ fontSize: 14, color: 'var(--ba-text-light)', marginBottom: 12 }}>请选择转发到的聊天：</div>
            <div style={{ maxHeight: 350, overflow: 'auto', border: '1px solid var(--ba-border)', borderRadius: 8 }}>
              {forwardChats.map((chat) => (
                <div
                  key={chat.id}
                  onClick={() => { setForwardChatId(chat.id); setForwardChatType(chat.type) }}
                  style={{ padding: '12px 16px', cursor: 'pointer', borderBottom: '1px solid var(--ba-border)', background: forwardChatId === chat.id ? 'var(--ba-blue-light)' : 'transparent', color: forwardChatId === chat.id ? 'var(--ba-blue)' : 'inherit', fontWeight: forwardChatId === chat.id ? 600 : 400, transition: 'background 0.15s' }}
                >
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 4 }}>
                    <span style={{ fontSize: 14 }}>{chat.name}</span>
                    {chat.type === 'group' && (
                      <span style={{ fontSize: 11, background: 'var(--ba-blue-light)', color: 'var(--ba-blue)', padding: '1px 6px', borderRadius: 4 }}>群聊</span>
                    )}
                  </div>
                  {chat.lastMessage && (
                    <div style={{ fontSize: 12, color: 'var(--ba-text-light)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{chat.lastMessage}</div>
                  )}
                </div>
              ))}
              {forwardChats.length === 0 && (
                <div style={{ padding: 24, textAlign: 'center', color: 'var(--ba-text-light)' }}>没有可转发的聊天</div>
              )}
            </div>
            <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
              <button className="ba-btn ba-btn-secondary" style={{ flex: 1 }} onClick={() => setShowForwardModal(false)}>取消</button>
              <button className="ba-btn ba-btn-primary" style={{ flex: 1 }} onClick={handleConfirmForward} disabled={!forwardChatId}>
                {!forwardChatId ? '请选择聊天' : `转发到${forwardChatType === 'group' ? '群聊' : '私聊'}`}
              </button>
            </div>
          </div>
        </div>
      </Modal>
    </div>
  )
}
