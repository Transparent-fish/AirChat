<script setup lang="ts">
import { ref, onMounted, nextTick } from 'vue'
import { 
  Send, 
  FolderOpen, 
  MessageSquare, 
  ShieldCheck, 
  LogOut,
  Download,
  Terminal,
  Plus,
  Camera,
  LogIn,
  FolderUp,
  X,
  Trash2,
  Settings,
  ShieldAlert,
  Ban,
  Lock
} from 'lucide-vue-next'
import { authApi, adminApi, fileApi } from './api'
import { watch } from 'vue'
import Markdown from './components/Markdown.vue'

// --- 类型定义 ---
interface Message {
  type: 'system' | 'user' | 'role_update'
  sender?: string
  sender_name?: string
  content?: string
  time?: string
  avatar?: string
  role?: 'user' | 'admin' | 'system'
}

interface SharedFile {
  name: string
  dirKey?: string
  size: number
  isDir: boolean
  owner?: string
}

// --- 状态管理 ---
const messages = ref<Message[]>([])
const inputMsg = ref('')
const socket = ref<WebSocket | null>(null)
const chatContainer = ref<HTMLElement | null>(null)
const activeTab = ref<'chat' | 'files'>('chat')
const files = ref<SharedFile[]>([])
const currentPath = ref('')

const showShareUI = ref(false)
const mySharedFolders = ref<string[]>([])
const isUploading = ref(false)

// 管理面板
const showAdminUI = ref(false)
const adminTab = ref<'users' | 'ips' | 'system'>('users')
const adminUsers = ref<any[]>([])
const bannedIPs = ref<any[]>([])
const newBanIP = ref('')
const newBanIPEnd = ref('')
const newAdminPassword = ref('')
const newSystemPassword = ref('')

const fetchAdminUsers = async () => {
  try {
    const res = await adminApi.getUsers()
    adminUsers.value = res.data
  } catch (e) {
    console.error(e)
  }
}

const canManage = (targetUser: any) => {
  if (currentUser.value.role === 'system') {
    return targetUser.role !== 'system'
  }
  if (currentUser.value.role === 'admin') {
    return targetUser.role === 'user'
  }
  return false
}

const toggleMute = async (user: any) => {
  if (!canManage(user)) return
  try {
    await adminApi.muteUser({ username: user.username, is_muted: !user.is_muted })
    await fetchAdminUsers()
  } catch (e) {
    alert('操作失败: ' + (e as any).response?.data?.error)
  }
}

const toggleBan = async (user: any) => {
  if (!canManage(user)) return
  try {
    await adminApi.banUser({ username: user.username, is_banned: !user.is_banned })
    await fetchAdminUsers()
  } catch (e) {
    alert('操作失败: ' + (e as any).response?.data?.error)
  }
}

const toggleRole = async (user: any) => {
  if (currentUser.value.role !== 'system' || user.role === 'system') return
  const targetRole = user.role === 'admin' ? 'user' : 'admin'
  try {
    await adminApi.setRole({ username: user.username, role: targetRole })
    await fetchAdminUsers()
  } catch (e) {
    alert('分配失败: ' + (e as any).response?.data?.error)
  }
}

const handleDeleteUser = async (user: any) => {
  if (!confirm(`确定要彻底删除用户 ${user.username} 吗？此操作不可撤销。`)) return
  if (!canManage(user)) return
  try {
    await adminApi.deleteUser(user.username)
    await fetchAdminUsers()
  } catch (e) {
    alert('删除失败: ' + (e as any).response?.data?.error)
  }
}

const fetchBannedIPs = async () => {
  try {
    const res = await adminApi.getBannedIPs()
    bannedIPs.value = res.data
  } catch (e) {}
}

const handleBanIP = async () => {
  if (!newBanIP.value) return
  let finalIP = newBanIP.value.trim()
  if (newBanIPEnd.value.trim()) {
    finalIP = `${finalIP}-${newBanIPEnd.value.trim()}`
  }
  
  try {
    await adminApi.banIP({ ip: finalIP, action: 'ban' })
    newBanIP.value = ''
    newBanIPEnd.value = ''
    await fetchBannedIPs()
  } catch (e) {
    alert('封禁失败: ' + (e as any).response?.data?.error)
  }
}

const handleUnbanIP = async (ip: string) => {
  try {
    await adminApi.banIP({ ip, action: 'unban' })
    await fetchBannedIPs()
  } catch (e) {
    alert('解封失败')
  }
}

const handleChangePassword = async () => {
  if (!newAdminPassword.value) return
  try {
    await adminApi.changePassword({ new_password: newAdminPassword.value })
    newAdminPassword.value = ''
    alert('基础管理密码修改成功')
  } catch (e) {
    alert('修改失败: ' + (e as any).response?.data?.error)
  }
}

const handleChangeSystemPassword = async () => {
  if (!newSystemPassword.value) return
  try {
    await adminApi.changeSystemPassword({ new_password: newSystemPassword.value })
    newSystemPassword.value = ''
    alert('超级系统密码修改成功')
  } catch (e) {
    alert('修改失败: ' + (e as any).response?.data?.error)
  }
}

watch(showAdminUI, (val) => {
  if (val) {
    adminTab.value = 'users'
    fetchAdminUsers()
    fetchBannedIPs()
  }
})

// 认证状态
const isLogined = ref(!!localStorage.getItem('airchat_token'))
const authMode = ref<'login' | 'register'>('login')
const authForm = ref({
  username: '',
  password: ''
})
const authError = ref('')

// 用户信息
const currentUser = ref({
  name: localStorage.getItem('airchat_name') || '',
  avatar: localStorage.getItem('airchat_avatar') || '',
  role: localStorage.getItem('airchat_role') || 'user'
})

// --- 核心逻辑 ---

const getAvatarUrl = (url: string) => {
  if (!url) return ''
  if (url.startsWith('/uploads/')) {
    return `http://${window.location.hostname}:8080${url}`
  }
  return url
}

const fetchMyShares = async () => {
  try {
    const res = await fileApi.getMyFolders()
    mySharedFolders.value = res.data || []
  } catch (e) {
    console.error('获取分享列表失败', e)
  }
}

const deleteShare = async (folder: string) => {
  try {
    await fileApi.deleteFolder(folder)
    await fetchMyShares()
    fetchFiles() // refresh public files
  } catch (e) {
    alert('删除失败')
  }
}

const onShareFolder = async (e: Event) => {
  const fileList = (e.target as HTMLInputElement).files
  if (!fileList || fileList.length === 0) return
  
  const firstFile = fileList[0]
  if (!firstFile) return
  const folderName = firstFile.webkitRelativePath?.split('/')[0] || 'UnknownFolder'
  
  const formData = new FormData()
  formData.append('folderName', folderName)
  for (let i = 0; i < fileList.length; i++) {
    const file = fileList[i]
    if (!file) continue
    formData.append('files', file)
    formData.append('paths', file.webkitRelativePath || file.name)
  }

  isUploading.value = true
  try {
    await fileApi.uploadFolder(formData)
    await fetchMyShares()
    fetchFiles()
    alert('分享成功！')
  } catch (err) {
    alert('上传失败')
  } finally {
    isUploading.value = false
    ;(e.target as HTMLInputElement).value = ''
  }
}

watch(showShareUI, (val) => {
  if (val) {
    fetchMyShares()
  }
})

const handleAuth = async () => {
  authError.value = ''
  try {
    if (authMode.value === 'register') {
      await authApi.register(authForm.value)
      authMode.value = 'login'
      alert('注册成功，请登录')
    } else {
      const res = await authApi.login(authForm.value)
      localStorage.setItem('airchat_token', res.data.token)
      localStorage.setItem('airchat_name', res.data.username)
      localStorage.setItem('airchat_avatar', res.data.avatar)
      localStorage.setItem('airchat_role', res.data.role)
      
      currentUser.value = {
        name: res.data.username,
        avatar: res.data.avatar,
        role: res.data.role
      }
      isLogined.value = true
      connectWS()
    }
  } catch (err: any) {
    authError.value = err.response?.data?.error || '认证失败'
  }
}

const logout = () => {
  localStorage.clear()
  isLogined.value = false
  if (socket.value) socket.value.close()
}

const scrollToBottom = async () => {
  await nextTick()
  if (chatContainer.value) {
    chatContainer.value.scrollTop = chatContainer.value.scrollHeight
  }
}

const connectWS = () => {
  if (!isLogined.value) return
  const token = localStorage.getItem('airchat_token')
  socket.value = new WebSocket(`ws://${window.location.hostname}:8080/ws?token=${token}`)

  socket.value.onmessage = (event) => {
    const data = JSON.parse(event.data)
    if (data.type === 'role_update') {
      currentUser.value.role = data.role
      localStorage.setItem('airchat_role', data.role)
      return
    }
    messages.value.push(data)
    scrollToBottom()
  }

  socket.value.onclose = () => {
    if (isLogined.value) {
      setTimeout(connectWS, 3000)
    }
  }
}

const sendMessage = () => {
  if (!inputMsg.value.trim() || !socket.value) return

  if (inputMsg.value.trim() === '/clear') {
    messages.value = []
    inputMsg.value = ''
    return
  }

  if (inputMsg.value.trim() === '/share') {
    activeTab.value = 'files'
    showShareUI.value = true
    inputMsg.value = ''
    return
  }

  const payload = {
    type: 'user',
    content: inputMsg.value
  }
  socket.value.send(JSON.stringify(payload))
  inputMsg.value = ''
}

onMounted(() => {
  if (isLogined.value) {
    connectWS()
    fetchFiles()
  }
})

// --- 头像更改 ---
const onAvatarChange = async (e: Event) => {
  const file = (e.target as HTMLInputElement).files?.[0]
  if (!file) return
  const formData = new FormData()
  formData.append('avatar', file)
  try {
    const res = await authApi.uploadAvatar(formData)
    const newUrl = res.data.url as string
    currentUser.value.avatar = newUrl
    localStorage.setItem('airchat_avatar', newUrl)
  } catch (err) {
    alert('头像上传失败')
  } finally {
    ;(e.target as HTMLInputElement).value = ''
  }
}

// --- 文件共享区 ---
const fetchFiles = async () => {
  try {
    const res = await fileApi.getSharedFolders(currentPath.value)
    files.value = (res.data || []).map((item: any) => ({
      name: item.name,
      dirKey: item.path, // 这里使用完整相对路径作为标识
      size: item.size || 0,
      isDir: item.is_dir,
      owner: item.owner || ''
    })).sort((a: any, b: any) => {
      // 文件夹优先
      if (a.isDir && !b.isDir) return -1
      if (!a.isDir && b.isDir) return 1
      // 同类型按名称排序
      return a.name.localeCompare(b.name)
    })
  } catch (e) {
    console.error('获取共享文件列表失败', e)
  }
}

const handleFileClick = (file: SharedFile) => {
  if (file.isDir) {
    currentPath.value = file.dirKey || ''
    fetchFiles()
  } else {
    downloadFile(file.dirKey || file.name)
  }
}

const goBack = () => {
  if (!currentPath.value) return
  const parts = currentPath.value.split('/')
  parts.pop()
  currentPath.value = parts.join('/')
  fetchFiles()
}

const downloadFile = (path: string) => {
  window.open(`http://${window.location.hostname}:8080/shared/${encodeURIComponent(path)}`, '_blank')
}

const formatSize = (size: number) => {
  if (!size) return '-'
  if (size < 1024) return size + ' B'
  if (size < 1024 * 1024) return (size / 1024).toFixed(1) + ' KB'
  return (size / (1024 * 1024)).toFixed(1) + ' MB'
}
</script>

<template>
  <!-- 登录/注册 覆盖层 -->
  <div v-if="!isLogined" class="fixed inset-0 z-50 flex items-center justify-center bg-slate-100/50 backdrop-blur-xl">
    <div class="w-full max-w-md p-8 glass rounded-3xl shadow-2xl border border-white/50 animate-in fade-in zoom-in duration-300">
      <div class="text-center mb-8">
        <div class="inline-flex p-4 bg-blue-600 rounded-2xl text-white mb-4 shadow-lg shadow-blue-200">
          <MessageSquare :size="32" />
        </div>
        <h2 class="text-3xl font-bold text-slate-800">AirChat</h2>
        <p class="text-slate-500 mt-2">{{ authMode === 'login' ? '欢迎回来' : '开启新的对话' }}</p>
      </div>

      <form @submit.prevent="handleAuth" class="space-y-4">
        <div>
          <label class="block text-sm font-medium text-slate-600 mb-1">用户名</label>
          <input 
            v-model="authForm.username"
            type="text" 
            placeholder="字母/数字/下划线, <=12位"
            class="w-full px-4 py-3 rounded-xl bg-white border border-slate-200 focus:ring-2 focus:ring-blue-100 outline-none transition-all"
            required
          />
        </div>
        <div>
          <label class="block text-sm font-medium text-slate-600 mb-1">密码</label>
          <input 
            v-model="authForm.password"
            type="password" 
            placeholder="请输入密码"
            class="w-full px-4 py-3 rounded-xl bg-white border border-slate-200 focus:ring-2 focus:ring-blue-100 outline-none transition-all"
            required
          />
        </div>
        <p v-if="authError" class="text-red-500 text-sm font-medium">{{ authError }}</p>
        <button 
          type="submit"
          class="w-full bg-blue-600 text-white py-3 rounded-xl font-bold shadow-lg shadow-blue-200 hover:bg-blue-700 active:scale-[0.98] transition-all flex items-center justify-center gap-2"
        >
          <LogIn :size="18" />
          {{ authMode === 'login' ? '登录' : '注册' }}
        </button>
      </form>

      <div class="mt-6 text-center">
        <button 
          @click="authMode = authMode === 'login' ? 'register' : 'login'"
          class="text-sm font-medium text-blue-600 hover:underline"
        >
          {{ authMode === 'login' ? '没有账号？立即注册' : '已有账号？返回登录' }}
        </button>
      </div>
    </div>
  </div>

  <!-- 分享管理 覆盖层 -->
  <div v-if="showShareUI" class="fixed inset-0 z-[60] flex items-center justify-center bg-slate-900/50 backdrop-blur-sm">
    <div class="w-full max-w-lg p-6 glass rounded-3xl shadow-2xl border border-white/50 animate-in fade-in zoom-in duration-200">
      <div class="flex justify-between items-center mb-6">
        <h3 class="text-xl font-bold flex items-center gap-2 text-slate-800">
          <FolderOpen class="text-blue-600" /> 分享管理
        </h3>
        <button @click="showShareUI = false" class="text-slate-400 hover:text-red-500 transition-colors">
          <X :size="24" />
        </button>
      </div>

      <div class="mb-4">
        <h4 class="text-xs font-bold text-slate-500 uppercase tracking-widest mb-2">我分享的文件夹</h4>
        <div v-if="mySharedFolders.length === 0" class="text-center py-6 text-slate-400 bg-white/40 rounded-2xl border border-dashed border-white/50">
          暂无分享
        </div>
        <ul v-else class="space-y-2 max-h-48 overflow-y-auto">
          <li v-for="folder in mySharedFolders" :key="folder" class="flex justify-between items-center p-3 bg-white/60 rounded-xl shadow-sm">
            <span class="font-bold text-slate-700 truncate w-3/4" :title="folder">{{ folder }}</span>
            <button @click="deleteShare(folder)" class="text-red-500 bg-white p-2 rounded-lg hover:bg-red-50 transition-colors shadow-sm" title="取消分享">
              <Trash2 :size="16" />
            </button>
          </li>
        </ul>
      </div>

      <div class="mt-6 border-t border-white/30 pt-4">
        <label class="w-full flex items-center justify-center gap-2 bg-blue-600 text-white py-3 rounded-xl font-bold shadow-lg shadow-blue-200 hover:bg-blue-700 active:scale-[0.98] transition-all cursor-pointer">
          <template v-if="isUploading">
            <span class="animate-pulse">上传中...请稍候</span>
          </template>
          <template v-else>
            <FolderUp :size="18" /> 选择文件夹并分享
            <input type="file" webkitdirectory directory multiple @change="onShareFolder" class="hidden" :disabled="isUploading" />
          </template>
        </label>
      </div>
    </div>
  </div>

  <!-- 管理面板 覆盖层 -->
  <div v-if="showAdminUI" class="fixed inset-0 z-[70] flex items-center justify-center bg-slate-900/50 backdrop-blur-sm">
    <div class="w-full max-w-4xl p-6 glass rounded-3xl shadow-2xl border border-white/50 animate-in fade-in zoom-in duration-200">
      <div class="flex justify-between items-center mb-6 border-b border-white/30 pb-4">
        <h3 class="text-2xl font-bold flex items-center gap-3 text-slate-800">
          <ShieldAlert class="text-blue-600" :size="28" /> 系统管理面板
        </h3>
        <button @click="showAdminUI = false" class="text-slate-400 hover:text-red-500 transition-colors">
          <X :size="28" />
        </button>
      </div>

      <div class="flex gap-6 min-h-[400px]">
        <!-- 左侧菜单 -->
        <div class="w-48 flex flex-col gap-2">
          <button @click="adminTab = 'users'" :class="adminTab === 'users' ? 'bg-blue-600 text-white shadow-md' : 'bg-white/50 text-slate-600 hover:bg-white/80'" class="px-4 py-3 rounded-xl font-bold text-left transition-all">用户管理</button>
          <button @click="adminTab = 'ips'" :class="adminTab === 'ips' ? 'bg-blue-600 text-white shadow-md' : 'bg-white/50 text-slate-600 hover:bg-white/80'" class="px-4 py-3 rounded-xl font-bold text-left transition-all">IP 封禁</button>
          <button @click="adminTab = 'system'" :class="adminTab === 'system' ? 'bg-blue-600 text-white shadow-md' : 'bg-white/50 text-slate-600 hover:bg-white/80'" class="px-4 py-3 rounded-xl font-bold text-left transition-all">系统设置</button>
        </div>

        <!-- 右侧内容 -->
        <div class="flex-1 bg-white/40 rounded-2xl p-6 border border-white/50 overflow-y-auto max-h-[60vh]">
          
          <!-- 用户管理 -->
          <div v-if="adminTab === 'users'">
            <h4 class="font-bold text-lg mb-4 text-slate-700">用户列表</h4>
            <div class="overflow-x-auto rounded-xl border border-white/50">
              <table class="w-full text-left text-sm whitespace-nowrap">
                <thead class="bg-white/60 text-slate-600">
                  <tr>
                    <th class="px-4 py-3 font-bold">头像</th>
                    <th class="px-4 py-3 font-bold">用户名</th>
                    <th class="px-4 py-3 font-bold">角色</th>
                    <th class="px-4 py-3 font-bold text-center">禁言</th>
                    <th class="px-4 py-3 font-bold text-center">封号</th>
                    <th class="px-4 py-3 font-bold text-center">账号管理</th>
                    <th v-if="currentUser.role === 'system'" class="px-4 py-3 font-bold text-center">系统权</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-white/40">
                  <tr v-for="user in adminUsers" :key="user.id" class="hover:bg-white/30 transition-colors">
                    <td class="px-4 py-2">
                      <img :src="getAvatarUrl(user.avatar || '')" class="w-8 h-8 rounded-full border border-white shadow-sm" />
                    </td>
                    <td class="px-4 py-2 font-medium break-words text-slate-700">{{ user.username }}</td>
                    <td class="px-4 py-2">
                      <span class="px-2 py-1 rounded-md text-[10px] uppercase font-bold" 
                            :class="user.role === 'system' ? 'bg-purple-100 text-purple-700' : (user.role === 'admin' ? 'bg-amber-100 text-amber-700' : 'bg-blue-100 text-blue-700')">
                        {{ user.role }}
                      </span>
                    </td>
                    <td class="px-4 py-2 text-center">
                      <button v-if="canManage(user)" @click="toggleMute(user)" :class="user.is_muted ? 'bg-red-500 hover:bg-red-600' : 'bg-green-500 hover:bg-green-600'" class="text-white px-3 py-1 rounded-lg font-bold text-xs shadow-sm transition-colors cursor-pointer">
                        {{ user.is_muted ? '解禁' : '禁言' }}
                      </button>
                      <span v-else class="text-slate-300 text-xs">-</span>
                    </td>
                    <td class="px-4 py-2 text-center">
                      <button v-if="canManage(user)" @click="toggleBan(user)" :class="user.is_banned ? 'bg-red-500 hover:bg-red-600' : 'bg-slate-500 hover:bg-slate-600'" class="text-white px-3 py-1 rounded-lg font-bold text-xs shadow-sm transition-colors cursor-pointer">
                        {{ user.is_banned ? '解封' : '封禁' }}
                      </button>
                      <span v-else class="text-slate-300 text-xs">-</span>
                    </td>
                    <td class="px-4 py-2 text-center">
                      <button v-if="canManage(user)" @click="handleDeleteUser(user)" class="bg-slate-100 hover:bg-red-100 text-slate-500 hover:text-red-600 p-2 rounded-lg transition-colors cursor-pointer inline-flex items-center justify-center">
                        <Trash2 :size="14" />
                      </button>
                      <span v-else class="text-slate-300 text-xs">-</span>
                    </td>
                    <td v-if="currentUser.role === 'system'" class="px-4 py-2 text-center">
                      <button v-if="user.role !== 'system'" @click="toggleRole(user)" :class="user.role === 'admin' ? 'bg-orange-400 hover:bg-orange-500' : 'bg-indigo-500 hover:bg-indigo-600'" class="text-white px-3 py-1 rounded-lg font-bold text-xs shadow-sm transition-colors cursor-pointer">
                        {{ user.role === 'admin' ? '降权' : '升权' }}
                      </button>
                      <span v-else class="text-slate-300 text-xs text-purple-500 font-bold tracking-widest uppercase">System</span>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </div>

          <!-- IP 封禁 -->
          <div v-else-if="adminTab === 'ips'">
            <h4 class="font-bold text-lg mb-4 text-slate-700">添加 IP/网段管控</h4>
            <div class="flex gap-2 mb-8 items-center bg-white/40 p-3 rounded-2xl border border-white/50">
              <input v-model="newBanIP" placeholder="如 192.168.1.1、192.168.0.0/24 或起始IP" class="flex-1 px-4 py-2.5 rounded-xl border border-white/50 bg-white/80 focus:ring-2 focus:ring-red-200 outline-none transition-all shadow-inner font-medium text-sm" @keyup.enter="handleBanIP"/>
              <span class="text-slate-400 font-bold px-1">—</span>
              <input v-model="newBanIPEnd" placeholder="结束IP (可选，若填则按范围封禁)" class="flex-1 px-4 py-2.5 rounded-xl border border-white/50 bg-white/80 focus:ring-2 focus:ring-red-200 outline-none transition-all shadow-inner font-medium text-sm" @keyup.enter="handleBanIP"/>
              <button @click="handleBanIP" class="bg-red-500 hover:bg-red-600 text-white px-5 py-2.5 rounded-xl font-bold shadow-lg shadow-red-200 flex items-center gap-2 transition-all active:scale-95 cursor-pointer ml-2">
                <Ban :size="16" /> 应用封禁约束
              </button>
            </div>

            <h4 class="font-bold text-lg mb-4 text-slate-700">已封禁列表</h4>
            <div v-if="bannedIPs.length === 0" class="text-slate-400 text-center py-6 bg-white/20 rounded-xl">暂无封禁记录</div>
            <ul v-else class="space-y-2">
              <li v-for="ban in bannedIPs" :key="ban.ip" class="flex justify-between items-center bg-white/50 p-3 rounded-xl shadow-sm border border-white/50">
                <div class="flex items-center gap-3 text-slate-700 font-medium">
                  <Terminal :size="16" class="text-red-500" />
                  {{ ban.ip }} <span v-if="ban.is_range" class="text-[10px] bg-red-100 text-red-600 px-2 py-0.5 rounded-full uppercase ml-2 tracking-widest shadow-sm">群体网段</span>
                </div>
                <button @click="handleUnbanIP(ban.ip)" class="text-white bg-slate-400 hover:bg-slate-500 px-3 py-1 rounded-lg text-xs font-bold transition-colors cursor-pointer">
                  解封
                </button>
              </li>
            </ul>
          </div>

          <!-- 系统设置 -->
          <div v-else-if="adminTab === 'system'" class="space-y-6">
             <!-- 基础管理密码 -->
             <div class="bg-white/50 p-5 rounded-2xl border border-white/50 shadow-sm transition-all hover:bg-white/70">
               <label class="block text-sm font-bold text-slate-600 mb-3 flex items-center gap-2">
                 <ShieldCheck class="text-amber-500" :size="16" /> 修改管理员 (Admin) 通用密码
               </label>
               <div class="flex gap-3">
                 <div class="relative flex-1">
                   <Lock class="absolute left-3.5 top-3 text-slate-400" :size="18" />
                   <input v-model="newAdminPassword" type="password" placeholder="输入新的 Admin 密码" class="w-full pl-11 pr-4 py-2.5 rounded-xl border border-white/50 bg-white/60 focus:ring-2 focus:ring-amber-200 outline-none transition-all shadow-inner font-medium text-sm" @keyup.enter="handleChangePassword" />
                 </div>
                 <button @click="handleChangePassword" class="bg-amber-500 hover:bg-amber-600 text-white px-6 py-2.5 rounded-xl font-bold shadow-lg shadow-amber-200 transition-all active:scale-95 cursor-pointer">
                   保存
                 </button>
               </div>
               <p class="text-xs text-amber-700/80 mt-3 flex items-start gap-1 font-medium bg-amber-500/10 p-2.5 rounded-lg">
                 用户可使用 `/admin <新密码>` 获得该等级管理权限。
               </p>
             </div>

             <!-- 超级管理密码 (仅限 System ) -->
             <div v-if="currentUser.role === 'system'" class="bg-white/50 p-5 rounded-2xl border border-purple-200/60 shadow-sm transition-all hover:bg-white/70">
               <label class="block text-sm font-bold text-slate-600 mb-3 flex items-center gap-2">
                 <ShieldAlert class="text-purple-500" :size="16" /> 修改系统最高权限 (System) 通用密码
               </label>
               <div class="flex gap-3">
                 <div class="relative flex-1">
                   <Lock class="absolute left-3.5 top-3 text-slate-400" :size="18" />
                   <input v-model="newSystemPassword" type="password" placeholder="输入新的 System 密码" class="w-full pl-11 pr-4 py-2.5 rounded-xl border border-white/50 bg-white/60 focus:ring-2 focus:ring-purple-200 outline-none transition-all shadow-inner font-medium text-sm" @keyup.enter="handleChangeSystemPassword" />
                 </div>
                 <button @click="handleChangeSystemPassword" class="bg-purple-600 hover:bg-purple-700 text-white px-6 py-2.5 rounded-xl font-bold shadow-lg shadow-purple-200 transition-all active:scale-95 cursor-pointer">
                   保存设定
                 </button>
               </div>
               <p class="text-[11px] text-purple-700/80 mt-3 flex items-start gap-1 font-bold bg-purple-500/10 p-2.5 rounded-lg uppercase tracking-widest break-words">
                 极高危操作。修改后凭 `/system <密码>` 获取该权限，具有任命 Admin 和无限制封禁所有用户的能力。
               </p>
             </div>

          </div>

        </div>
      </div>
    </div>
  </div>

  <div class="flex h-screen overflow-hidden bg-slate-50">
    <!-- 侧边栏 -->
    <aside class="w-72 glass flex flex-col border-r border-slate-200/50 z-10 m-4 rounded-3xl shadow-xl overflow-hidden">
      <div class="p-6">
        <div class="flex items-center gap-3 mb-8">
          <div class="bg-blue-600 p-2 rounded-xl text-white shadow-lg shadow-blue-200">
            <MessageSquare :size="20" />
          </div>
          <h1 class="text-xl font-bold tracking-tight text-slate-800">AirChat</h1>
        </div>

        <nav class="space-y-2">
          <button 
            @click="activeTab = 'chat'"
            :class="activeTab === 'chat' ? 'bg-blue-600 text-white shadow-lg shadow-blue-200' : 'text-slate-600 hover:bg-white/50'"
            class="w-full flex items-center gap-3 px-4 py-3 rounded-xl transition-all duration-300 font-medium group"
          >
            <MessageSquare :size="18" />
            公共频道
          </button>

          <button 
            @click="activeTab = 'files'"
            :class="activeTab === 'files' ? 'bg-blue-600 text-white shadow-lg shadow-blue-200' : 'text-slate-600 hover:bg-white/50'"
            class="w-full flex items-center gap-3 px-4 py-3 rounded-xl transition-all duration-300 font-medium group"
          >
            <FolderOpen :size="18" />
            资源共享库
          </button>
          
          <button 
            v-if="currentUser.role === 'admin' || currentUser.role === 'system'"
            @click="showAdminUI = true"
            class="w-full flex items-center gap-3 px-4 py-3 rounded-xl transition-all duration-300 font-medium group text-slate-600 hover:bg-white/50 mt-2"
          >
            <Settings :size="18" />
            系统管理终端
          </button>
        </nav>
      </div>

      <div class="mt-auto p-4">
        <div class="p-4 rounded-2xl bg-white/60 backdrop-blur-sm border border-white/50 shadow-sm relative group/profile">
          <button @click="logout" class="absolute -top-2 -right-2 bg-white p-1.5 rounded-full shadow-md text-slate-400 hover:text-red-500 transition-colors">
            <LogOut :size="14" />
          </button>
          
          <div class="flex items-center gap-3 mb-1">
            <div class="relative cursor-pointer overflow-hidden rounded-full w-12 h-12 bg-slate-200 group-hover:ring-2 ring-blue-400 transition-all">
              <img :src="getAvatarUrl(currentUser.avatar)" class="w-full h-full object-cover" alt="avatar" />
              <label class="absolute inset-0 bg-black/40 flex items-center justify-center opacity-0 group-hover/profile:opacity-100 transition-opacity cursor-pointer">
                <Camera class="text-white" :size="16" />
                <input type="file" @change="onAvatarChange" class="hidden" accept="image/*" />
              </label>
            </div>
        <div class="overflow-hidden leading-tight flex flex-col justify-center">
              <p class="font-bold text-slate-800 truncate">{{ currentUser.name }}</p>
              <div class="flex items-center gap-1 mt-0.5">
                <ShieldAlert v-if="currentUser.role === 'system'" :size="12" class="text-purple-500 shrink-0" />
                <ShieldCheck v-else-if="currentUser.role === 'admin'" :size="12" class="text-amber-500 shrink-0" />
                <span class="text-[10px] uppercase font-bold tracking-widest leading-none mt-px" 
                      :class="currentUser.role === 'system' ? 'text-purple-500/80' : 'text-slate-400'">{{ currentUser.role }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </aside>

    <!-- 主界面 -->
    <main class="flex-1 flex flex-col min-w-0 bg-transparent m-4 ml-0">
      <div class="flex-1 flex flex-col glass rounded-3xl shadow-2xl border border-white/20 overflow-hidden relative">
        
        <!-- 背景装饰 -->
        <div class="absolute -top-24 -right-24 w-64 h-64 bg-blue-400/10 rounded-full blur-3xl pointer-events-none"></div>
        <div class="absolute -bottom-24 -left-24 w-64 h-64 bg-purple-400/10 rounded-full blur-3xl pointer-events-none"></div>

        <!-- 聊天内容页 -->
        <template v-if="activeTab === 'chat'">
          <header class="h-20 flex items-center px-8 border-b border-white/20 bg-white/30 backdrop-blur-sm relative z-10">
            <div class="flex items-center gap-3">
              <div class="w-3 h-3 rounded-full bg-green-500 shadow-[0_0_10px_rgba(34,197,94,0.5)]"></div>
              <div>
                <h2 class="font-bold text-slate-800">实验室公共频道</h2>
                <p class="text-[10px] text-slate-500 font-medium">REALTIME CHAT COMPONENT</p>
              </div>
            </div>
          </header>

          <div ref="chatContainer" class="flex-1 overflow-y-auto p-6 space-y-6 relative z-10">
            <transition-group name="list">
              <div v-for="(msg, index) in messages" :key="index" class="flex flex-col">
                <!-- 系统消息 -->
                <div v-if="msg.type === 'system'" class="flex justify-center my-4">
                  <div class="bg-white/40 backdrop-blur-sm text-slate-500 text-[10px] px-4 py-1.5 rounded-full font-bold border border-white/50 flex items-center gap-2 shadow-sm">
                    <Terminal :size="10" />
                    {{ msg.content }}
                  </div>
                </div>

                <!-- 普通消息 -->
                <div 
                  v-else 
                  class="flex gap-4 group" 
                  :class="msg.sender_name === currentUser.name ? 'flex-row-reverse' : ''"
                >
                  <!-- 头像 -->
                  <div class="flex-shrink-0 relative">
                    <img :src="getAvatarUrl(msg.avatar || '')" class="w-11 h-11 rounded-2xl border-2 border-white/50 shadow-md object-cover transition-transform group-hover:scale-105" />
                    <!-- System 超级管理角标 -->
                    <div v-if="msg.role === 'system'" class="absolute -top-1.5 -right-1.5 bg-purple-500 border-2 border-white p-0.5 rounded-full text-white shadow-md">
                      <ShieldAlert :size="12" />
                    </div>
                    <!-- Admin 管理角标 -->
                    <div v-else-if="msg.role === 'admin'" class="absolute -top-1.5 -right-1.5 bg-amber-400 border-2 border-white p-0.5 rounded-full text-white shadow-md">
                      <ShieldCheck :size="12" />
                    </div>
                  </div>

                  <!-- 气泡内容 -->
                  <div class="flex flex-col max-w-[75%]" :class="msg.sender_name === currentUser.name ? 'items-end' : 'items-start'">
                    <div class="flex items-center gap-2 mb-1 px-1">
                      <span v-if="msg.role === 'system'" class="px-1.5 py-0.5 rounded-md bg-purple-500 text-white text-[8px] font-black uppercase tracking-widest shadow-sm">System</span>
                      <span v-else-if="msg.role === 'admin'" class="px-1.5 py-0.5 rounded-md bg-amber-400 text-white text-[8px] font-black uppercase tracking-widest shadow-sm">Admin</span>
                      <span class="text-xs font-bold text-slate-700">{{ msg.sender_name }}</span>
                      <span class="text-[9px] text-slate-400 font-bold uppercase tracking-tighter">{{ msg.time }}</span>
                    </div>
                    
                    <div 
                      class="px-5 py-3 rounded-2xl text-[14px] leading-relaxed shadow-lg transition-all border break-words"
                      :class="msg.sender_name === currentUser.name 
                        ? 'bg-blue-600 text-white border-blue-500 rounded-tr-none' 
                        : 'bg-white/80 backdrop-blur-sm text-slate-700 border-white/20 rounded-tl-none'"
                    >
                      <Markdown :content="msg.content || ''" />
                    </div>
                  </div>
                </div>
              </div>
            </transition-group>
          </div>

          <footer class="p-6 bg-white/30 backdrop-blur-md border-t border-white/20 z-10">
            <div class="max-w-4xl mx-auto flex gap-3 p-2 bg-white/70 rounded-2xl border border-white focus-within:ring-4 focus-within:ring-blue-100/50 focus-within:bg-white transition-all duration-500 shadow-xl shadow-slate-200/50">
              <input 
                v-model="inputMsg"
                @keyup.enter="sendMessage"
                type="text" 
                placeholder="发送消息 (支持 Markdown 和 LaTeX)..."
                class="flex-1 bg-transparent px-4 py-2 text-sm focus:outline-none placeholder-slate-400 font-medium"
              />
              <button 
                @click="sendMessage"
                class="bg-blue-600 text-white p-2.5 rounded-xl hover:bg-blue-700 active:scale-95 transition-all shadow-lg shadow-blue-200/50 flex items-center justify-center"
              >
                <Send :size="18" />
              </button>
            </div>
          </footer>
        </template>

        <!-- 文件浏览器页 -->
        <template v-else>
          <header class="h-20 flex items-center justify-between px-8 border-b border-white/20 bg-white/30 backdrop-blur-sm z-10">
            <div class="flex items-center gap-3">
              <FolderOpen class="text-blue-600" :size="24" />
              <div>
                <h2 class="font-bold text-slate-800 text-lg">局域网共享资源</h2>
                <p class="text-[10px] text-slate-500 font-medium uppercase tracking-widest">Local Network Storage</p>
              </div>
            </div>
            <div class="flex items-center gap-4">
              <button v-if="currentPath" @click="goBack" class="bg-white/50 backdrop-blur-sm border border-white px-3 py-2 rounded-xl text-slate-600 font-bold text-xs hover:bg-white transition-all shadow-sm flex items-center gap-2">
                返回上一级
              </button>
              <button @click="fetchFiles" class="bg-white/50 backdrop-blur-sm border border-white px-4 py-2 rounded-xl text-slate-600 font-bold text-xs hover:bg-white transition-all shadow-sm flex items-center gap-2">
                <Plus :size="14" class="rotate-45" /> 刷新
              </button>
            </div>
          </header>

          <div class="flex-1 overflow-y-auto p-8 z-10">
            <div v-if="files.length === 0" class="flex flex-col items-center justify-center h-full text-slate-400 py-20 border-4 border-dashed border-white/30 rounded-[3rem] bg-white/10">
              <FolderOpen :size="64" class="mb-4 opacity-10" />
              <p class="font-bold">共享文件夹目前为空</p>
              <p class="text-[10px] mt-1 uppercase tracking-widest opacity-60">Add files to backend/shared directory</p>
            </div>

            <div v-else class="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-6">
              <div 
                v-for="file in files" :key="file.name"
                class="p-6 rounded-[2rem] bg-white/60 backdrop-blur-md border border-white/50 shadow-sm hover:shadow-2xl hover:-translate-y-2 transition-all duration-500 group relative overflow-hidden"
              >
                <div class="absolute inset-x-0 bottom-0 h-1 transition-all duration-700" :class="file.isDir ? 'bg-blue-600/0 group-hover:bg-blue-600/100' : 'bg-emerald-500/0 group-hover:bg-emerald-500/100'"></div>
                
                <div class="flex items-start justify-between mb-6">
                  <div 
                    @click="handleFileClick(file)"
                    :class="file.isDir ? 'bg-blue-600 shadow-blue-200' : 'bg-emerald-500 shadow-emerald-200'"
                    class="p-4 rounded-2xl text-white shadow-lg group-hover:scale-110 transition-transform cursor-pointer"
                  >
                    <FolderOpen v-if="file.isDir" :size="20" />
                    <Download v-else :size="20" />
                  </div>
                  <button 
                    v-if="!file.isDir"
                    @click="downloadFile(file.dirKey || file.name)"
                    class="p-2.5 bg-white text-slate-400 hover:text-emerald-600 rounded-xl transition-all shadow-sm border border-slate-100"
                  >
                    <Download :size="16" />
                  </button>
                </div>

                <h3 
                  @click="handleFileClick(file)"
                  class="font-black text-slate-800 truncate mb-1 text-sm tracking-tight cursor-pointer transition-colors"
                  :class="file.isDir ? 'hover:text-blue-600' : 'hover:text-emerald-500'" 
                  :title="file.name"
                >
                  {{ file.name }}
                </h3>
                <div class="flex items-center justify-between text-[10px] font-black text-slate-400 uppercase tracking-widest">
                  <span>{{ file.isDir ? '目录' : '文件' }}</span>
                  <span class="bg-slate-100 px-2 py-0.5 rounded-full">{{ formatSize(file.size) }}</span>
                </div>
              </div>
            </div>
          </div>
        </template>
      </div>
    </main>
  </div>
</template>

<style>
/* 引入动画 */
.list-enter-active,
.list-leave-active {
  transition: all 0.5s cubic-bezier(0.25, 0.46, 0.45, 0.94);
}
.list-enter-from {
  opacity: 0;
  transform: translateY(30px) scale(0.9);
}
.list-leave-to {
  opacity: 0;
  transform: scale(0.9);
}

/* 毛玻璃增强 */
.glass {
  background: rgba(255, 255, 255, 0.4);
  backdrop-filter: blur(25px);
  -webkit-backdrop-filter: blur(25px);
}

/* 自定义滚动条 */
::-webkit-scrollbar {
  width: 4px;
}
::-webkit-scrollbar-thumb {
  background: rgba(59, 130, 246, 0.3);
  border-radius: 10px;
}
::-webkit-scrollbar-thumb:hover {
  background: rgba(59, 130, 246, 0.5);
}

/* Markdown 数学公式修正 */
.katex-display {
  margin: 0.5em 0;
}
</style>
