import { Pipe, PipeTransform } from '@angular/core';

@Pipe({
  name: 'autoScale'
})
export class AutoScalePipe implements PipeTransform {

  transform(value: any, args?: any): any {
    return (value / 128).toFixed(2) + ' Kb';
  }

}
