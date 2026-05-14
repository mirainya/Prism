import React, { useEffect, useState } from 'react';
import { fetchChannels } from '../services/api';
import { Channel } from '../types';
import ChatModelSection from './capabilities/ChatModelSection';

const ChatModels: React.FC = () => {
    const [channels, setChannels] = useState<Channel[]>([]);
    useEffect(() => { fetchChannels().then(setChannels); }, []);
    return <ChatModelSection channels={channels} />;
};

export default ChatModels;
