import { HttpClient } from '@angular/common/http';
import { Injectable } from '@angular/core';
import { firstValueFrom } from 'rxjs';

@Injectable({
  providedIn: 'root'
})
export class ChannelRequestService {

  constructor(private http: HttpClient) {}

  submitRequest(name: string, email: string, desiredSlug: string, description: string): Promise<{ status: string; id: string }> {
    return firstValueFrom(
      this.http.post<{ status: string; id: string }>('/api/channel-request', { name, email, desiredSlug, description })
    );
  }
}
