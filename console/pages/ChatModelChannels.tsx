import React, {useEffect, useMemo, useState} from 'react';
import {Link2, Plus, RefreshCw, Search} from 'lucide-react';
import {
    createChatModelChannel,
    deleteChatModelChannel,
    fetchChannels,
    fetchChatModelChannels,
    fetchChatModels,
    updateChatModelChannel,
} from '../services/api';
import {Channel, ChatModel, ChatModelChannel} from '../types';
import ChatModelChannelGroupCard, {ChatModelChannelGroup} from '../components/ChatModelChannelGroupCard';
import ChatModelChannelModal, {
    ChatModelChannelFormData,
    ChatModelChannelModalDefaults,
} from '../components/ChatModelChannelModal';

const normalizeText = (value: string | undefined | null) => (value || '').toLowerCase();

const ChatModelChannels: React.FC = () => {
    const [mappings, setMappings] = useState<ChatModelChannel[]>([]);
    const [chatModels, setChatModels] = useState<ChatModel[]>([]);
    const [channels, setChannels] = useState<Channel[]>([]);
    const [loading, setLoading] = useState(true);
    const [refreshing, setRefreshing] = useState(false);
    const [modalOpen, setModalOpen] = useState(false);
    const [editingMapping, setEditingMapping] = useState<ChatModelChannel | null>(null);
    const [modalDefaults, setModalDefaults] = useState<ChatModelChannelModalDefaults | undefined>(undefined);
    const [filterModel, setFilterModel] = useState('');
    const [filterChannel, setFilterChannel] = useState('');
    const [searchTerm, setSearchTerm] = useState('');
    const [expandedGroups, setExpandedGroups] = useState<Set<string>>(new Set());

    const loadMappings = async (showRefreshing = false) => {
        if (showRefreshing) {
            setRefreshing(true);
        }
        try {
            const mappingsData = await fetchChatModelChannels();
            setMappings(mappingsData);
        } finally {
            if (showRefreshing) {
                setRefreshing(false);
            }
        }
    };

    const loadInitialData = async () => {
        setLoading(true);
        try {
            const [mappingsData, modelsData, channelsData] = await Promise.all([
                fetchChatModelChannels(),
                fetchChatModels(),
                fetchChannels(),
            ]);
            setMappings(mappingsData);
            setChatModels(modelsData);
            setChannels(channelsData);
        } finally {
            setLoading(false);
        }
    };

    useEffect(() => {
        loadInitialData();
    }, []);

    useEffect(() => {
        if (filterModel) {
            setExpandedGroups(prev => new Set(prev).add(filterModel));
        }
    }, [filterModel]);

    const filteredMappings = useMemo(() => {
        const keyword = searchTerm.trim().toLowerCase();

        return mappings.filter(mapping => {
            const matchModel = !filterModel || mapping.modelCode === filterModel;
            const matchChannel = !filterChannel || String(mapping.channelId) === filterChannel;
            const matchKeyword = !keyword || [
                mapping.modelCode,
                mapping.chatModel?.name,
                mapping.vendorModel,
                mapping.channel?.name,
                mapping.channel?.type,
            ].some(field => normalizeText(field).includes(keyword));

            return matchModel && matchChannel && matchKeyword;
        });
    }, [filterChannel, filterModel, mappings, searchTerm]);

    const groupedMappings = useMemo<ChatModelChannelGroup[]>(() => {
        const groups = new Map<string, ChatModelChannelGroup>();

        filteredMappings.forEach(mapping => {
            const existing = groups.get(mapping.modelCode);
            if (existing) {
                existing.mappings.push(mapping);
                if (mapping.status === 1) {
                    existing.enabledCount += 1;
                }
                return;
            }

            groups.set(mapping.modelCode, {
                modelCode: mapping.modelCode,
                modelName: mapping.chatModel?.name || mapping.modelCode,
                provider: mapping.chatModel?.provider,
                mappings: [mapping],
                enabledCount: mapping.status === 1 ? 1 : 0,
            });
        });

        return Array.from(groups.values()).sort((a, b) => a.modelName.localeCompare(b.modelName));
    }, [filteredMappings]);

    const resetFilters = () => {
        setFilterModel('');
        setFilterChannel('');
        setSearchTerm('');
    };

    const openCreateModal = (defaults?: ChatModelChannelModalDefaults) => {
        setEditingMapping(null);
        setModalDefaults(defaults);
        setModalOpen(true);
    };

    const handleCreate = () => {
        openCreateModal({
            modelCode: filterModel || undefined,
            channelId: filterChannel ? Number(filterChannel) : undefined,
        });
    };

    const handleCreateFromGroup = (modelCode: string) => {
        openCreateModal({
            modelCode,
            channelId: filterChannel ? Number(filterChannel) : undefined,
        });
    };

    const handleEdit = (mapping: ChatModelChannel) => {
        setEditingMapping(mapping);
        setModalDefaults(undefined);
        setModalOpen(true);
    };

    const handleSave = async (data: ChatModelChannelFormData) => {
        if (editingMapping) {
            await updateChatModelChannel(editingMapping.id, data);
        } else {
            await createChatModelChannel(data);
            if (data.model_code) {
                setExpandedGroups(prev => new Set(prev).add(data.model_code));
            }
        }
        await loadMappings();
    };

    const handleDelete = async (id: number) => {
        if (!confirm('确定删除该渠道映射?')) {
            return;
        }
        await deleteChatModelChannel(id);
        await loadMappings();
    };

    const handleToggleStatus = async (mapping: ChatModelChannel) => {
        const newStatus = mapping.status === 1 ? 0 : 1;
        await updateChatModelChannel(mapping.id, {status: newStatus});
        await loadMappings();
    };

    const toggleGroup = (modelCode: string) => {
        setExpandedGroups(prev => {
            const next = new Set(prev);
            if (next.has(modelCode)) {
                next.delete(modelCode);
            } else {
                next.add(modelCode);
            }
            return next;
        });
    };

    if (loading) {
        return (
            <div className="flex items-center justify-center h-64">
                <div className="animate-spin rounded-full h-8 w-8 border-b-2 border-indigo-600"></div>
            </div>
        );
    }

    return (
        <div className="p-6 max-w-7xl mx-auto space-y-6">
            <div className="flex items-center justify-between gap-4">
                <div className="flex items-center gap-3">
                    <div className="p-2 bg-indigo-100 rounded-lg">
                        <Link2 className="text-indigo-600" size={24}/>
                    </div>
                    <div>
                        <h1 className="text-xl font-bold text-gray-900">模型渠道映射</h1>
                        <p className="text-sm text-gray-500">按模型查看并管理渠道映射关系</p>
                    </div>
                </div>
                <div className="flex items-center gap-2">
                    <button
                        onClick={() => loadMappings(true)}
                        className="flex items-center gap-2 px-3 py-2 text-sm text-gray-600 hover:text-gray-900 rounded-lg hover:bg-gray-100 transition-colors"
                    >
                        <RefreshCw size={16} className={refreshing ? 'animate-spin' : ''}/>
                        刷新映射
                    </button>
                    <button
                        onClick={handleCreate}
                        className="flex items-center gap-2 px-4 py-2 bg-indigo-600 text-white rounded-lg hover:bg-indigo-700 transition-colors"
                    >
                        <Plus size={20}/>
                        添加映射
                    </button>
                </div>
            </div>

            <div className="bg-white rounded-2xl shadow-sm border border-gray-100 p-4">
                <div className="flex flex-col lg:flex-row gap-4">
                    <div className="relative flex-1">
                        <Search size={18} className="absolute left-3 top-1/2 -translate-y-1/2 text-gray-400"/>
                        <input
                            type="text"
                            value={searchTerm}
                            onChange={e => setSearchTerm(e.target.value)}
                            placeholder="搜索模型、供应商模型、渠道名或类型..."
                            className="w-full pl-10 pr-4 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 text-sm"
                        />
                    </div>
                    <div className="lg:w-60">
                        <select
                            value={filterModel}
                            onChange={e => setFilterModel(e.target.value)}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 text-sm"
                        >
                            <option value="">全部模型</option>
                            {chatModels.map(model => (
                                <option key={model.code} value={model.code}>{model.name}</option>
                            ))}
                        </select>
                    </div>
                    <div className="lg:w-60">
                        <select
                            value={filterChannel}
                            onChange={e => setFilterChannel(e.target.value)}
                            className="w-full px-3 py-2 border border-gray-200 rounded-lg focus:outline-none focus:ring-2 focus:ring-indigo-500 text-sm"
                        >
                            <option value="">全部渠道</option>
                            {channels.map(channel => (
                                <option key={channel.id} value={channel.id}>{channel.name}</option>
                            ))}
                        </select>
                    </div>
                    <button
                        type="button"
                        onClick={resetFilters}
                        className="px-4 py-2 border border-gray-200 rounded-lg text-sm text-gray-700 hover:bg-gray-50 transition-colors"
                    >
                        重置筛选
                    </button>
                </div>
            </div>

            <div className="space-y-4">
                {groupedMappings.map(group => (
                    <ChatModelChannelGroupCard
                        key={group.modelCode}
                        group={group}
                        expanded={expandedGroups.has(group.modelCode) || filterModel === group.modelCode}
                        onToggle={() => toggleGroup(group.modelCode)}
                        onAdd={handleCreateFromGroup}
                        onEdit={handleEdit}
                        onDelete={handleDelete}
                        onToggleStatus={handleToggleStatus}
                    />
                ))}

                {groupedMappings.length === 0 && (
                    <div className="bg-white rounded-2xl border border-gray-100 shadow-sm px-6 py-12 text-center text-gray-500">
                        {filterModel || filterChannel || searchTerm ? '没有找到匹配的渠道映射' : '暂无渠道映射数据'}
                    </div>
                )}
            </div>

            <ChatModelChannelModal
                isOpen={modalOpen}
                channelMapping={editingMapping}
                chatModels={chatModels}
                channels={channels}
                defaults={modalDefaults}
                onClose={() => {
                    setModalOpen(false);
                    setModalDefaults(undefined);
                }}
                onSave={handleSave}
            />
        </div>
    );
};

export default ChatModelChannels;
