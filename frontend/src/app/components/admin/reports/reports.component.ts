import { Component, Input, OnInit } from '@angular/core';
import { NbButtonModule, NbCardModule, NbToastrService } from "@nebular/theme";
import { AdminService } from '../../../services/admin.service';
import { Reports, Report } from '../../../models/report.model';
import { SlugService } from '../../../services/slug.service';

import { MessageTimePipe } from "../../../pipes/message-time.pipe";

@Component({
  selector: 'app-reports',
  imports: [
    NbCardModule,
    NbButtonModule,
    MessageTimePipe
],
  templateUrl: './reports.component.html',
  styleUrl: './reports.component.scss'
})
export class ReportsComponent implements OnInit {

  reports: Reports = [];
  @Input() status: 'open' | 'closed' | 'all' = 'open';

  constructor(
    private adminService: AdminService,
    private toastrService: NbToastrService,
    private slugService: SlugService,
  ) { }

  ngOnInit(): void {
    this.adminService.getReports(this.status).then(reports => {
      this.reports = reports;
    })
      .catch(() => this.toastrService.danger('', 'אירעה שגיאה בעת טעינת הדיווחים'));
  }

  toggleReport(report: Report) {
    // The flip used to be applied before the request and never undone, so a
    // 404/500 left the button reading "reopen" for a report the server still
    // has open. Send the intended state and apply it only once it is stored.
    const updated: Report = { ...report, closed: !report.closed, updatedAt: new Date() };
    this.adminService.setReports(updated).then(() => {
      this.toastrService.success('', updated.closed ? 'דיווח נסגר בהצלחה' : 'דיווח נפתח מחדש');
      if (this.status === 'all') {
        const i = this.reports.findIndex(r => r.id === report.id);
        if (i >= 0) this.reports[i] = updated;
      } else {
        this.reports = this.reports.filter(r => r.id !== report.id);
      }
    }).catch((err: any) => this.toastrService.danger('',
      err?.status === 404 ? 'הדיווח לא נמצא' : 'שגיאה בעדכון הדיווח'));
  }

  viewReport(messageId: number) {
    window.open(`${window.location.origin}/channel/${this.slugService.slug}#${messageId}`, '_blank');
  }
}
