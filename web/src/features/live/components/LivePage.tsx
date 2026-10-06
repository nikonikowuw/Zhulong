import React from 'react';
import { LiveDashboard } from './LiveDashboard';

export const LivePage: React.FC = () => {
  return (
    <div className="w-full flex flex-col animate-in fade-in duration-200">
      <LiveDashboard />
    </div>
  );
};
