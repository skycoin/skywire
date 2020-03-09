import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { ViewAllLinkComponent } from './view-all-link.component';

describe('ViewAllLinkComponent', () => {
  let component: ViewAllLinkComponent;
  let fixture: ComponentFixture<ViewAllLinkComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ ViewAllLinkComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(ViewAllLinkComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
