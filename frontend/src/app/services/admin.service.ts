import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { BehaviorSubject, firstValueFrom, Observable, Subject } from 'rxjs';
import { ChatFile, ChatMessage } from './chat.service';
import { ResponseResult } from '../models/response-result.model';
import { Setting } from '../models/setting.model';
import { Reports, Report } from '../models/report.model';
import { Statistics } from '../models/statistics.model';
import { SlugService } from './slug.service';

export interface ChannelUser {
  email: string;
  role: 'owner' | 'moderator' | 'writer' | '';
}

export type EditMsg = {
  new?: boolean;
  isScheduling?: boolean;
  message: ChatMessage;
}

@Injectable({
  providedIn: 'root'
})
export class AdminService {
  private messageEdit = new BehaviorSubject<EditMsg | undefined>(undefined);
  messageEditObservable = this.messageEdit.asObservable();

  private schedulingBus = new Subject<void>();
  schedulingBusObservable = this.schedulingBus.asObservable();

  private schedulingMessages: ChatMessage[] | null = null;

  enterSendsMessage: boolean = false;

  constructor(
    private http: HttpClient,
    private slugService: SlugService,
  ) { }

  private get slug() { return this.slugService.slug; }

  clearCache() {
    this.schedulingMessages = null;
  }

  reloadSchedulingMessage() {
    this.schedulingBus.next();
  }

  setEditMessage(edit: EditMsg | undefined) {
    this.messageEdit.next(edit);
  }

  getEditMessage(): EditMsg | undefined {
    return this.messageEdit.value;
  }

  getStatistics(): Promise<Statistics> {
    return firstValueFrom(this.http.get<Statistics>(`/api/channel/${this.slug}/admin/statistics`));
  }

  resetPeakStatistics(): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>('/api/super-admin/statistics/reset', {}));
  }

  addMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>(`/api/channel/${this.slug}/admin/new`, message);
  }

  editMessage(message: ChatMessage): Observable<ChatMessage> {
    return this.http.post<ChatMessage>(`/api/channel/${this.slug}/admin/edit-message`, message);
  }

  deleteMessage(id: number | undefined): Observable<ChatMessage> {
    return this.http.get<ChatMessage>(`/api/channel/${this.slug}/admin/delete-message/${id}`);
  }

  uploadFile(formData: FormData) {
    return this.http.post<ChatFile>(`/api/channel/${this.slug}/admin/upload`, formData, {
      reportProgress: true,
      observe: 'events',
      responseType: 'json'
    });
  }

  getChannelUsers(): Promise<ChannelUser[]> {
    return firstValueFrom(this.http.get<ChannelUser[]>(`/api/channel/${this.slug}/admin/users/get`));
  }

  setChannelUsers(users: ChannelUser[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/users/set`, { users }));
  }

  setEmojis(emojis: string[] | undefined) {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/set-emojis`, { emojis }));
  }

  getSettings(): Promise<Setting[]> {
    return firstValueFrom(this.http.get<Setting[]>(`/api/channel/${this.slug}/admin/settings/get`));
  }

  setSettings(settings: Setting[]): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/settings/set`, settings));
  }

  getReports(status: string): Promise<Reports> {
    return firstValueFrom(this.http.get<Reports>(`/api/channel/${this.slug}/admin/reports/get`, {
      params: {
        status: status
      }
    }));
  }

  setReports(report: Report): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/reports/set`, report));
  }

  async getScheduledMessages(reload?: boolean): Promise<ChatMessage[]> {
    if (this.schedulingMessages && !reload) {
      return this.schedulingMessages;
    }

    try {
      this.schedulingMessages = await firstValueFrom(this.http.get<ChatMessage[]>(`/api/channel/${this.slug}/admin/scheduled-messages/get`));
      return this.schedulingMessages;
    } catch {
      return this.schedulingMessages || [];
    }
  }

  setScheduledMessage(message: ChatMessage): Promise<ResponseResult> {
    this.schedulingMessages?.unshift(message);
    return this.updateSchedulingMessages();
  }

  editScheduledMessage(message: ChatMessage): Promise<ResponseResult> {
    if (message.id === undefined || !this.schedulingMessages) return Promise.reject('Message ID is undefined');
    this.schedulingMessages[message.id] = message;
    return this.updateSchedulingMessages();
  }

  deleteScheduledMessage(id: number | undefined): Promise<ResponseResult> {
    if (id === undefined || !this.schedulingMessages) return Promise.reject('Message ID is undefined');
    this.schedulingMessages.splice(id, 1);
    return this.updateSchedulingMessages();
  }

  private updateSchedulingMessages(): Promise<ResponseResult> {
    return firstValueFrom(this.http.post<ResponseResult>(`/api/channel/${this.slug}/admin/scheduled-messages/update`, this.schedulingMessages));
  }
}
