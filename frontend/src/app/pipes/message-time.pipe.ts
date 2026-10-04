import { Pipe, PipeTransform } from '@angular/core';
import moment from 'moment';
import 'moment/locale/he'

@Pipe({
  name: 'messageTime'
})
export class MessageTimePipe implements PipeTransform {

  transform(value: any | string, ...args: unknown[]): any {
    let m = moment(value);
    const today = moment().startOf('day');
    const messageDay = m.clone().startOf('day');
    const daysDiff = messageDay.diff(today, 'days');
    switch (true) {
      case daysDiff > 0: // Future
        // The he locale's sameElse is 'L' (date only), so a message scheduled
        // more than six days ahead lost its time of day in the list.
        return m.calendar(null, { sameElse: 'L LT' });
      case daysDiff == 0: // Today
        return m.format('LT');
      case daysDiff == -1: // Yesterday
        return m.calendar();
      case daysDiff >= -6: // This week
        return `${m.format('dddd')} ${m.format('LT')}`;
      default:
        return m.format('L LT');
    }
  }

}
