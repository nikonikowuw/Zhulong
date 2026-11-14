export interface VehicleAttributes {
  plateNumber: string
  plateColor: string
  vehicleType: string
}

export function extractVehicleAttributes(
  attributes?: Record<string, unknown>
): VehicleAttributes {
  return {
    plateNumber:
      typeof attributes?.plate_number === 'string'
        ? attributes.plate_number
        : '',
    plateColor:
      typeof attributes?.plate_color === 'string' ? attributes.plate_color : '',
    vehicleType:
      typeof attributes?.vehicle_type === 'string'
        ? attributes.vehicle_type
        : '',
  }
}
