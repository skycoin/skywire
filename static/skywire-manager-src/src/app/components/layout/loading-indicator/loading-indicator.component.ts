import { Component, HostBinding, Input } from '@angular/core';

@Component({
  selector: 'app-loading-indicator',
  templateUrl: './loading-indicator.component.html',
  styleUrls: ['./loading-indicator.component.scss']
})
export class LoadingIndicatorComponent {
  @HostBinding('class') get class() { return 'full-width full-height flex'; }

  @Input() showWhite = true;
}
