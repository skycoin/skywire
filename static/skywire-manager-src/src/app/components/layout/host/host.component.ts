import {Component, ComponentFactoryResolver, Input, Type, ViewChild, AfterViewInit} from '@angular/core';
import {ComponentHostDirective} from '../../../directives/component-host.directive';

@Component({
  selector: 'app-host',
  templateUrl: './host.component.html',
  styleUrls: ['./host.component.css']
})
export class HostComponent implements AfterViewInit {
  @Input() componentClass: Type<any>;
  @Input() data: any;
  @ViewChild(ComponentHostDirective, { static: false }) host: ComponentHostDirective;

  constructor(
    private componentFactoryResolver: ComponentFactoryResolver
  ) { }

  ngAfterViewInit() {
    const componentFactory = this.componentFactoryResolver.resolveComponentFactory(this.componentClass);

    const viewContainerRef = this.host.viewContainerRef;
    viewContainerRef.clear();
    const comp = viewContainerRef.createComponent(componentFactory);

    comp.instance.data = this.data;
  }
}
