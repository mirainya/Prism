export interface Theme {
  key: string
  name: string
  color: string
}

export const themes: Theme[] = [
  { key: 'lavender', name: '薰衣草', color: '#8b5cf6' },
  { key: 'sakura', name: '樱花', color: '#f472b6' },
  { key: 'mint', name: '薄荷', color: '#10b981' },
  { key: 'sky', name: '天空', color: '#3b82f6' },
  { key: 'milktea', name: '奶茶', color: '#a8896c' },
]
