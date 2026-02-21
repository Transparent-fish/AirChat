import axios from 'axios'

const API_BASE = `http://${window.location.hostname}:8080/api`

const api = axios.create({
    baseURL: API_BASE
})

// 请求拦截器处理 Token
api.interceptors.request.use(config => {
    const token = localStorage.getItem('airchat_token')
    if (token) {
        config.headers.Authorization = `Bearer ${token}`
    }
    return config
})

export const authApi = {
    register: (data: any) => api.post('/register', data),
    login: (data: any) => api.post('/login', data),
    uploadAvatar: (formData: FormData) => api.post('/upload-avatar', formData, {
        headers: { 'Content-Type': 'multipart/form-data' }
    })
}

export const fileApi = {
    uploadFolder: (formData: FormData) => api.post('/upload-folder', formData, {
        headers: { 'Content-Type': 'multipart/form-data' }
    }),
    getMyFolders: () => api.get('/my-folders'),
    deleteFolder: (folderName: string) => api.delete(`/delete-folder/${encodeURIComponent(folderName)}`)
}

export const adminApi = {
    getUsers: () => api.get('/admin/users'),
    muteUser: (data: { username: string, is_muted: boolean }) => api.post('/admin/mute', data),
    banUser: (data: { username: string, is_banned: boolean }) => api.post('/admin/ban_user', data),
    getBannedIPs: () => api.get('/admin/banned_ips'),
    banIP: (data: { ip: string, action: 'ban' | 'unban' }) => api.post('/admin/ban_ip', data),
    changePassword: (data: { new_password: string }) => api.post('/admin/password', data),
    setRole: (data: { username: string, role: string }) => api.post('/admin/set_role', data),
    changeSystemPassword: (data: { new_password: string }) => api.post('/admin/system_password', data)
}

export default api
