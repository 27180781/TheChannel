import { Injectable } from '@angular/core';
import { HttpClient } from '@angular/common/http';
import { firstValueFrom, Observable } from 'rxjs';
import { Channel, ChannelFeatureFlags } from '../models/channel.model';
import { ResponseResult } from '../models/response-result.model';
import { SlugService } from './slug.service';

export type MessageType = 'md' | 'text' | 'image' | 'video' | 'audio' | 'document' | 'other';
export type Reactions = { [key: string]: number }
export interface ChatMessage {
  id?: number;
  type?: MessageType;
  text?: string;
  timestamp?: Date;
  userId?: number | null;
  author?: string;
  authorId?: string;
  last_edit?: Date;
  deleted?: boolean;
  file?: ChatFile;
  views?: number;
  reactions?: Reactions;
  is_ads?: boolean;
}
export type ChatResponse = ChatMessage[];

export interface ChatFile {
  url: string;
  filename: string;
  filetype: string;
  /**
   * Pixel size, sent by the upload endpoint only for images whose header it
   * could read. Absent for every other upload, and for anything uploaded
   * before the backend started recording it.
   */
  width?: number;
  height?: number;
}

export interface Attachment {
  file: File;
  url?: string;
  uploadProgress?: number;
  uploading?: boolean;
  embedded?: string;
}

@Injectable({
  providedIn: 'root'
})
export class ChatService {
  private eventSource!: EventSource;
  // null = not loaded yet; an empty array is a legitimately empty, loaded list.
  private emojis: string[] | null = null;
  private emojisRequest?: Promise<string[]>;
  public channelInfo?: Channel;
  private channelInfoRequest?: Promise<void>;

  constructor(private http: HttpClient, private slugService: SlugService) { }

  private get slug() { return this.slugService.slug; }

  updateChannelInfo(): Promise<void> {
    // Deduplicated: several components need the channel info (and the feature
    // flags it carries) on the same first paint.
    //
    // The slug is captured when the request is issued and re-checked on
    // arrival: a channel switch can leave the previous channel's /info still in
    // flight, and if it resolves after the new channel's it would otherwise
    // overwrite the wrong channel's info (name, feature flags, require-auth).
    const requestedSlug = this.slug;
    this.channelInfoRequest ??= firstValueFrom(this.http.get<Channel>(`/api/channel/${requestedSlug}/info`))
      .then(info => { if (requestedSlug === this.slug) this.channelInfo = info; })
      .finally(() => { this.channelInfoRequest = undefined; });
    return this.channelInfoRequest;
  }

  // Resolves once the feature flags are known, without issuing a second request
  // when the header already has one in flight.
  ensureChannelInfo(): Promise<void> {
    return this.channelInfo ? Promise.resolve() : this.updateChannelInfo();
  }

  // A missing features object means an older backend or a response cached before
  // the flags existed — treat it as "everything enabled" so the UI is never
  // stripped by an answer that simply does not mention the feature.
  isFeatureEnabled(feature: keyof ChannelFeatureFlags): boolean {
    return this.channelInfo?.features?.[feature] !== false;
  }

  get reactionsEnabled(): boolean { return this.isFeatureEnabled('reactions'); }
  get fileUploadsEnabled(): boolean { return this.isFeatureEnabled('fileUploads'); }
  get reportsEnabled(): boolean { return this.isFeatureEnabled('reports'); }
  get scheduledMessagesEnabled(): boolean { return this.isFeatureEnabled('scheduledMessages'); }

  editChannelInfo(name: string, description: string, logoUrl: string): Observable<ResponseResult> {
    return this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/edit-channel-info`, { name, description, logoUrl });
  }

  getMessages(offset: number, limit: number, direction: string): Observable<ChatResponse> {
    return this.http.get<ChatResponse>(`/api/channel/${this.slug}/messages`, {
      params: {
        offset: offset.toString(),
        limit: limit.toString(),
        direction: direction
      }
    });
  }

  setReact(messageId: number, react: string) {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/reactions/set-reactions`, { messageId, emoji: react }));
  }

  clearCache() {
    this.emojis = null;
    this.emojisRequest = undefined;
    this.channelInfo = undefined;
    this.channelInfoRequest = undefined;
  }

  async getEmojisList(reload: boolean = false): Promise<string[]> {
    if (this.emojis && !reload) return this.emojis;
    // Deduplicated: every message component asks for the list on first paint.
    // Same slug guard as updateChannelInfo — without it, a request for the
    // previous channel resolving after a switch would cache that channel's
    // reaction set under the new slug. clearCache also drops any in-flight
    // request on switch so a forced reload actually re-fetches.
    const requestedSlug = this.slug;
    this.emojisRequest ??= firstValueFrom(this.http.get<string[]>(`/api/channel/${requestedSlug}/emojis/list`))
      .then(list => { if (requestedSlug === this.slug) this.emojis = list; return list; })
      .finally(() => { this.emojisRequest = undefined; });
    return this.emojisRequest;
  }

  reportMessage(messageId: number, reason: string): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/messages/report`, { messageId, reason }));
  }

  sseListener(): EventSource {
    if (this.eventSource) {
      this.eventSource.close();
    }

    this.eventSource = new EventSource(`/api/channel/${this.slug}/events`);

    this.eventSource.onopen = () => {
      console.log('Connection opened');
    };

    this.eventSource.onerror = (error) => {
      console.error('EventSource failed:', error);
    };

    return this.eventSource;
  }

  sseClose() {
    if (this.eventSource) {
      this.eventSource.close();
    }
  }
}
