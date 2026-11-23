export interface Statistics {
    usersAmount: number;
    connectedUsersAmount: number;
    /** Distinct people (signed-in email, or a guest's address) behind those connections. */
    connectedPeopleAmount?: number;
    /** The channel is locked to its allowlist: guests cannot connect at all. */
    viewersOnly?: boolean;
    peakSSEConnections: PeakSSEConnections;
    connectionsStatistics: ConnectionsStatistics;
}

export interface PeakSSEConnections {
    value: number;
    timestamp: Date;
}

export interface ConnectionsStatistics {
    date: number[];
    labels: string[];
}
