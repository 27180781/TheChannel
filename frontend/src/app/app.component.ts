import { Component, OnInit } from '@angular/core';
import { RouterOutlet } from '@angular/router';
import { NbCardModule, NbLayoutModule } from "@nebular/theme";
import { NotificationsService } from './services/notifications.service';
import { CelebrationComponent } from './components/shared/celebration.component';
import { OfflineBannerComponent } from './components/shared/offline-banner.component';

@Component({
  selector: 'app-root',
  imports: [
    RouterOutlet,
    NbLayoutModule,
    NbCardModule,
    CelebrationComponent,
    OfflineBannerComponent,
],
  templateUrl: './app.component.html',
  styleUrl: './app.component.scss'
})
export class AppComponent implements OnInit {

  constructor(
    private notificationsService: NotificationsService,
  ) { }

  ngOnInit(): void {
    this.notificationsService.init();
  }
}
