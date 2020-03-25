import { async, ComponentFixture, TestBed } from '@angular/core/testing';

import { SkysocksSettingsComponent } from './skysocks-settings.component';

describe('SkysocksSettingsComponent', () => {
  let component: SkysocksSettingsComponent;
  let fixture: ComponentFixture<SkysocksSettingsComponent>;

  beforeEach(async(() => {
    TestBed.configureTestingModule({
      declarations: [ SkysocksSettingsComponent ]
    })
    .compileComponents();
  }));

  beforeEach(() => {
    fixture = TestBed.createComponent(SkysocksSettingsComponent);
    component = fixture.componentInstance;
    fixture.detectChanges();
  });

  it('should create', () => {
    expect(component).toBeTruthy();
  });
});
